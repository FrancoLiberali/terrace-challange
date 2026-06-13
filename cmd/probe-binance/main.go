// Command probe-binance is a thin CLI wrapper around the binance package.
// It fetches the slippage-aware effective ETH-USDC prices from Binance for a
// fixed set of trade sizes and prints them. The probe stays in the repo as
// an ongoing diagnostic tool — see plan.md.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/cex/binance"
)

const requestTimeout = 10 * time.Second

var tradeSizes = []decimal.Decimal{
	decimal.NewFromInt(1),
	decimal.NewFromInt(10),
	decimal.NewFromInt(100),
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("probe-binance: %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := binance.NewClient(binance.DefaultBaseURL)
	quotes, err := client.EffectivePrices(ctx, binance.SymbolETHUSDC, tradeSizes)
	if err != nil {
		return fmt.Errorf("fetch effective prices: %w", err)
	}
	printQuotes(os.Stdout, tradeSizes, quotes)
	return nil
}

// printQuotes renders the per-size effective prices. The Quotes slice is laid
// out as interleaved Buy/Sell rows by size (the order EffectivePrices
// guarantees), so a direct indexed loop is enough. The smallest configured
// size effectively reads the top of the book.
func printQuotes(w io.Writer, sizes []decimal.Decimal, quotes []binance.Quote) {
	fmt.Fprintln(w, "Binance ETH-USDC effective prices (slippage-aware):")
	fmt.Fprintf(w, "  %-14s   %-22s   %-22s\n", "Size", "BUY (eat asks)", "SELL (eat bids)")
	for i, sz := range sizes {
		fmt.Fprintf(w, "  %-14s   %-22s   %-22s\n",
			sz.String()+" ETH",
			formatQuote(quotes[2*i]),
			formatQuote(quotes[2*i+1]),
		)
	}
}

func formatQuote(q binance.Quote) string {
	if q.Err != nil {
		return "insufficient depth"
	}
	return "$" + q.Price.StringFixed(4) + "/ETH"
}
