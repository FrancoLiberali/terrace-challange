// Command probe-uniswap is a thin CLI wrapper around the uniswapv3 package.
// It runs one eth_call per (size, side) against the QuoterV2 contract for
// the 0.3% ETH-USDC pool and prints the slippage-aware effective per-unit
// prices. The probe stays in the repo as an ongoing diagnostic tool —
// see plan.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/dex/uniswapv3"
	"github.com/FrancoLiberali/terrace-challenge/internal/pricing"
)

const requestTimeout = 20 * time.Second

var tradeSizes = []decimal.Decimal{
	decimal.NewFromInt(1),
	decimal.NewFromInt(10),
	decimal.NewFromInt(100),
}

func main() {
	if err := run(); err != nil {
		slog.Error("probe-uniswap exiting with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	if err := godotenv.Load(); err != nil {
		return fmt.Errorf("load .env: %w", err)
	}
	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		return errors.New("ETH_RPC_URL is not set in .env (see README.md)")
	}
	quoterRaw := os.Getenv("UNISWAP_QUOTER_ADDRESS")
	if quoterRaw == "" {
		return errors.New("UNISWAP_QUOTER_ADDRESS is not set in .env (see README.md)")
	}
	if !common.IsHexAddress(quoterRaw) {
		return fmt.Errorf("invalid UNISWAP_QUOTER_ADDRESS %q: not a hex-encoded address", quoterRaw)
	}
	feeRaw := os.Getenv("UNISWAP_POOL_FEE")
	if feeRaw == "" {
		return errors.New("UNISWAP_POOL_FEE is not set in .env (see README.md)")
	}
	fee, err := strconv.ParseUint(feeRaw, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid UNISWAP_POOL_FEE %q: %w", feeRaw, err)
	}

	client, err := uniswapv3.NewClient(rpcURL, common.HexToAddress(quoterRaw))
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	pool := uniswapv3.Pool{Base: uniswapv3.WETH, Quote: uniswapv3.USDC, Fee: uint32(fee)}
	quotes, err := client.EffectivePrices(ctx, pool, tradeSizes)
	if err != nil {
		return fmt.Errorf("fetch effective prices: %w", err)
	}
	printQuotes(os.Stdout, tradeSizes, quotes)
	return nil
}

// printQuotes renders the per-size effective prices. Buy[i] and Sell[i]
// correspond to sizes[i]. The smallest configured size effectively reads
// the pool's spot price (modulo the 0.3% fee).
func printQuotes(w io.Writer, sizes []decimal.Decimal, quotes pricing.Quotes) {
	fmt.Fprintln(w, "Uniswap V3 ETH-USDC (0.3% pool) effective prices (slippage-aware):")
	fmt.Fprintf(w, "  %-14s   %-22s   %-22s\n", "Size", "BUY (USDC→ETH)", "SELL (ETH→USDC)")
	for i, sz := range sizes {
		fmt.Fprintf(w, "  %-14s   %-22s   %-22s\n",
			sz.String()+" ETH",
			formatQuote(quotes.Buy[i]),
			formatQuote(quotes.Sell[i]),
		)
	}
}

func formatQuote(q pricing.Quote) string {
	if q.Err != nil {
		return "error: " + q.Err.Error()
	}
	return "$" + q.Price.StringFixed(4) + "/ETH"
}
