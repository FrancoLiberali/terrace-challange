// Command arbd subscribes to Ethereum's newHeads stream, dispatches
// per-block snapshot work to the Binance and Uniswap V3 adapters in
// parallel, pairs the results via the Pathfinder, evaluates each
// candidate against the cost model, and prints structured arbitrage
// alerts above the configured threshold.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/alert"
	"github.com/FrancoLiberali/terrace-challenge/internal/arbitrage"
	"github.com/FrancoLiberali/terrace-challenge/internal/cex/binance"
	"github.com/FrancoLiberali/terrace-challenge/internal/chain"
	"github.com/FrancoLiberali/terrace-challenge/internal/dex/uniswapv3"
	"github.com/FrancoLiberali/terrace-challenge/internal/pathfinder"
	"github.com/FrancoLiberali/terrace-challenge/internal/pipeline"
	"github.com/FrancoLiberali/terrace-challenge/internal/resilience"
)

// Venue identifiers used across map keys, log fields, breaker labels,
// and the "venues" list — the canonical names the rest of the bot
// refers to each integration by.
const (
	venueBinance = "binance"
	venueUniswap = "uniswap"
)

// Hardcoded for now; configuration lands in Step 7.
var (
	tradeSizes = []decimal.Decimal{
		decimal.NewFromInt(1),
		decimal.NewFromInt(10),
		decimal.NewFromInt(100),
	}

	// Default cost model. Trading fees are NOT here: each adapter folds
	// its venue's intrinsic fees into the Price it returns (Binance's
	// taker fee via binance.Symbol.TakerFeeBps, Uniswap V3's 0.3% pool
	// fee already in QuoterV2's output). Gas units travel per-candidate
	// from QuoterV2's per-call gasEstimate. The model just carries the
	// profitability threshold.
	defaultCostModel = arbitrage.CostModel{
		MinNetProfitUSDC: decimal.NewFromInt(1),
	}
)

func main() {
	if err := run(); err != nil {
		slog.Error("arbd exiting with error", "err", err)
		os.Exit(1)
	}
}

type envConfig struct {
	httpURL string
	wsURL   string
	pretty  bool
	level   slog.Level
}

func loadEnv() (envConfig, error) {
	if err := godotenv.Load(); err != nil {
		return envConfig{}, fmt.Errorf("load .env: %w", err)
	}
	cfg := envConfig{
		httpURL: os.Getenv("ETH_RPC_URL"),
		wsURL:   os.Getenv("ETH_RPC_WS_URL"),
	}
	if cfg.httpURL == "" {
		return envConfig{}, errors.New("ETH_RPC_URL is not set in .env (see README.md)")
	}
	if cfg.wsURL == "" {
		return envConfig{}, errors.New("ETH_RPC_WS_URL is not set in .env (see README.md)")
	}
	if raw := os.Getenv("LOG_LEVEL"); raw != "" {
		if err := cfg.level.UnmarshalText([]byte(raw)); err != nil {
			return envConfig{}, fmt.Errorf("invalid LOG_LEVEL %q: %w", raw, err)
		}
	}
	if raw := os.Getenv("PRETTY_ALERTS"); raw != "" {
		p, err := strconv.ParseBool(raw)
		if err != nil {
			return envConfig{}, fmt.Errorf("invalid PRETTY_ALERTS %q: %w", raw, err)
		}
		cfg.pretty = p
	}
	return cfg, nil
}

func run() error {
	cfg, err := loadEnv()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(newSlogHandler(cfg.pretty, &slog.HandlerOptions{Level: cfg.level})))

	// sink: structured slog event for log aggregation + optional
	// multi-line block to stdout when PRETTY_ALERTS is on. The sink's
	// logger emits unconditionally (LOG_LEVEL must not be able to
	// suppress the bot's product); the default slog handler still
	// honors LOG_LEVEL for everything else.
	sink := &alert.TextSink{
		Logger: slog.New(newSlogHandler(cfg.pretty, nil)),
		Out:    os.Stdout,
		Pretty: cfg.pretty,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Subscriber → Dispatcher → Pathfinder is a three-stage pipeline,
	// each stage running in its own goroutine. The main goroutine
	// consumes candidates and applies the cost model inline.
	sub := chain.NewSubscriber(cfg.wsURL)
	binanceHTTP := resilience.NewHTTPClient(resilience.HTTPClientConfig{
		Retry:          resilience.DefaultRetryConfig(),
		Limiter:        resilience.NewRateLimiter(venueBinance, 5, 2),
		Breaker:        newBreaker(venueBinance),
		RequestTimeout: 10 * time.Second,
		Logger:         slog.Default(),
	})
	binanceClient := binance.NewClientWithHTTP(binance.DefaultBaseURL, binanceHTTP)
	binanceSn := pipeline.NewBinanceSnapshotter(binanceClient, binance.SymbolETHUSDC, tradeSizes)

	uniswapHTTP := resilience.NewHTTPClient(resilience.HTTPClientConfig{
		Retry:          resilience.DefaultRetryConfig(),
		Limiter:        resilience.NewRateLimiter(venueUniswap, 10, 10),
		Breaker:        newBreaker(venueUniswap),
		RequestTimeout: 10 * time.Second,
		Logger:         slog.Default(),
	})
	uniswapClient, err := uniswapv3.NewClientWithHTTP(cfg.httpURL, uniswapHTTP)
	if err != nil {
		return fmt.Errorf("connect to RPC: %w", err)
	}
	defer uniswapClient.Close()
	uniswapSn := pipeline.NewUniswapSnapshotter(uniswapClient, uniswapv3.PoolETHUSDC03, tradeSizes)

	disp := pipeline.NewDispatcher(map[string]pipeline.Snapshotter{
		venueBinance: binanceSn,
		venueUniswap: uniswapSn,
	})
	pf := pathfinder.NewPathfinder()
	ev := arbitrage.NewEvaluator(defaultCostModel)

	subErr := make(chan error, 1)
	go func() { subErr <- sub.Run(ctx) }()
	dispErr := make(chan error, 1)
	go func() { dispErr <- disp.Run(ctx, sub.Events()) }()
	pfErr := make(chan error, 1)
	go func() { pfErr <- pf.Run(ctx, disp.Results()) }()

	slog.Info("arbd starting",
		"venues", []string{venueBinance, venueUniswap},
		"pair", "ETH-USDC",
		"dex_pool", "uniswap_v3_0.3pct",
		"threshold_usdc", defaultCostModel.MinNetProfitUSDC.String(),
	)
	if cfg.pretty {
		fmt.Fprintf(os.Stdout,
			"arbd: detecting CEX↔DEX arbitrage on ETH-USDC (binance + uniswap v3 0.3%%)\n"+
				"      threshold: net profit > $%s USDC — Ctrl+C to stop\n\n",
			defaultCostModel.MinNetProfitUSDC.String(),
		)
	}

	consume(pf.Candidates(), ev, sink)

	return awaitShutdown(subErr, dispErr, pfErr)
}

func newSlogHandler(pretty bool, opts *slog.HandlerOptions) slog.Handler {
	if pretty {
		return slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.NewJSONHandler(os.Stderr, opts)
}

func newBreaker(venue string) *resilience.CircuitBreaker {
	return resilience.NewCircuitBreaker(resilience.BreakerConfig{
		Name:         venue,
		MinRequests:  20,
		FailureRatio: 0.2,
		Cooldown:     30 * time.Second,
		Interval:     time.Minute,
		OnStateChange: func(name, from, to string) {
			slog.Warn("circuit breaker state change", "venue", name, "from", from, "to", to)
		},
	})
}

func consume(candidates <-chan pathfinder.CandidatePath, ev *arbitrage.Evaluator, sink alert.Sink) {
	total, profitable := 0, 0
	for path := range candidates {
		total++
		op := ev.Evaluate(path)
		if !ev.IsProfitable(op) {
			continue
		}
		profitable++
		sink.Emit(op)
	}
	slog.Info("evaluation finished", "total_candidates", total, "profitable", profitable)
}

// awaitShutdown collects each pipeline stage's Run result. ctx-cancel
// errors are the expected clean-exit path (SIGINT); anything else is
// propagated to the caller.
func awaitShutdown(subErr, dispErr, pfErr <-chan error) error {
	if err := <-subErr; err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("subscriber: %w", err)
	}
	if err := <-dispErr; err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("dispatcher: %w", err)
	}
	if err := <-pfErr; err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("pathfinder: %w", err)
	}
	return nil
}
