// Command arbd is the day-one composition of the system: it subscribes
// to Ethereum's newHeads stream, dispatches per-block snapshot work to
// the Binance and Uniswap V3 adapters in parallel, and prints each
// venue's effective-price table as it arrives. Per architecture.md
// decision 2, results stream in independently — a slow venue does not
// delay the others, and the per-block arrival order reflects which
// venue happened to respond first. Pairing and arbitrage detection
// land in Step 5 (Pathfinder + Profitability Evaluator).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/cex/binance"
	"github.com/FrancoLiberali/terrace-challenge/internal/chain"
	"github.com/FrancoLiberali/terrace-challenge/internal/dex/uniswapv3"
	"github.com/FrancoLiberali/terrace-challenge/internal/pipeline"
	"github.com/FrancoLiberali/terrace-challenge/internal/pricing"
)

// tradeSizes match the per-probe configuration so the arbd output is
// directly comparable to probe-binance / probe-uniswap output for the
// same block. Configuration moves to a config file in Step 7.
var tradeSizes = []decimal.Decimal{
	decimal.NewFromInt(1),
	decimal.NewFromInt(10),
	decimal.NewFromInt(100),
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("arbd: %v", err)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	httpURL := os.Getenv("ETH_RPC_URL")
	if httpURL == "" {
		return errors.New("ETH_RPC_URL is not set in .env (see README.md)")
	}
	wsURL := os.Getenv("ETH_RPC_WS_URL")
	if wsURL == "" {
		return errors.New("ETH_RPC_WS_URL is not set in .env (see README.md)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Construct the chain subscriber.
	sub := chain.NewSubscriber(wsURL)

	// Construct both venue clients + their Snapshotter wrappers.
	binanceClient := binance.NewClient(binance.DefaultBaseURL)
	binanceSn := pipeline.NewBinanceSnapshotter(binanceClient, binance.SymbolETHUSDC, tradeSizes)

	uniswapClient, err := uniswapv3.NewClient(httpURL)
	if err != nil {
		return fmt.Errorf("connect to RPC: %w", err)
	}
	defer uniswapClient.Close()
	uniswapSn := pipeline.NewUniswapSnapshotter(uniswapClient, uniswapv3.PoolETHUSDC03, tradeSizes)

	// Wire the dispatcher with both venues.
	disp := pipeline.NewDispatcher(map[string]pipeline.Snapshotter{
		"binance": binanceSn,
		"uniswap": uniswapSn,
	})

	// Launch subscriber and dispatcher; they own their own lifecycles
	// and tear down on ctx cancellation.
	subErr := make(chan error, 1)
	go func() { subErr <- sub.Run(ctx) }()
	dispErr := make(chan error, 1)
	go func() { dispErr <- disp.Run(ctx, sub.Events()) }()

	fmt.Fprintln(os.Stdout, "arbd: subscribed to newHeads, dispatching to binance + uniswap — Ctrl+C to stop")

	// Consume results as they arrive. The range exits when the
	// Dispatcher closes its output channel (i.e., when its Run returns).
	for r := range disp.Results() {
		printResult(os.Stdout, r)
	}

	// Both Run goroutines have returned by the time we get here (the
	// Dispatcher closes Results last, and the Subscriber's Events
	// channel closure is what unblocked the Dispatcher).
	if err := <-subErr; err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("subscriber: %w", err)
	}
	if err := <-dispErr; err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("dispatcher: %w", err)
	}
	return nil
}

// printResult renders one venue's snapshot for one block. Stream order
// reflects when each venue responded — multiple lines for the same block
// will appear close together but not necessarily in a fixed order.
func printResult(w io.Writer, r pipeline.VenueResult) {
	fmt.Fprintf(w, "\nblock %-9d  %-7s  ts=%s  baseFee=%s gwei\n",
		r.Block.Number,
		r.Venue,
		r.Block.Timestamp.Format("15:04:05 MST"),
		formatGwei(r.Block.BaseFee),
	)
	if r.Err != nil {
		fmt.Fprintf(w, "  ERROR: %v\n", r.Err)
		return
	}
	printQuotes(w, r.Quotes)
}

// printQuotes prints the per-size buy/sell table. Buy[i] and Sell[i]
// always correspond to the same size (see pricing.Quotes).
func printQuotes(w io.Writer, q pricing.Quotes) {
	for i := range q.Buy {
		fmt.Fprintf(w, "  %-9s  buy %-22s  sell %s\n",
			q.Buy[i].Size.String()+" ETH",
			formatPrice(q.Buy[i]),
			formatPrice(q.Sell[i]),
		)
	}
}

func formatPrice(q pricing.Quote) string {
	if q.Err != nil {
		return "n/a (" + q.Err.Error() + ")"
	}
	return "$" + q.Price.StringFixed(4) + "/ETH"
}

// formatGwei prints a wei amount as a fixed-point gwei string (3 dp).
// Duplicated from probe-chain — small enough that a shared helper isn't
// worth a new package yet.
func formatGwei(wei *big.Int) string {
	if wei == nil {
		return "n/a"
	}
	const oneGweiInWei = 1_000_000_000
	gwei := new(big.Int).Mul(wei, big.NewInt(1000))
	gwei.Quo(gwei, big.NewInt(oneGweiInWei))
	whole := new(big.Int).Quo(gwei, big.NewInt(1000))
	frac := new(big.Int).Mod(gwei, big.NewInt(1000))
	return fmt.Sprintf("%s.%03d", whole.String(), frac.Int64())
}
