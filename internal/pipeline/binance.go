package pipeline

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/cex/binance"
	"github.com/FrancoLiberali/terrace-challenge/internal/chain"
	"github.com/FrancoLiberali/terrace-challenge/internal/pricing"
)

// BinanceSnapshotter binds a binance.Client to a specific symbol and
// trade-sizes set so it satisfies Snapshotter: one method, no
// venue-specific arguments at the call site.
//
// The block event is intentionally ignored — Binance's public REST API
// has no "as of block N" semantics, it returns the current orderbook.
// The argument stays on the interface for broker-readiness, not for any
// behavior in this implementation.
type BinanceSnapshotter struct {
	client *binance.Client
	symbol binance.Symbol
	sizes  []decimal.Decimal
}

// NewBinanceSnapshotter wraps client to satisfy the Snapshotter interface
// with the symbol and sizes bound at construction time.
func NewBinanceSnapshotter(client *binance.Client, symbol binance.Symbol, sizes []decimal.Decimal) *BinanceSnapshotter {
	return &BinanceSnapshotter{client: client, symbol: symbol, sizes: sizes}
}

// Snapshot fetches Binance's orderbook and walks it for the bound sizes.
func (b *BinanceSnapshotter) Snapshot(ctx context.Context, _ chain.BlockEvent) (pricing.Quotes, error) {
	return b.client.EffectivePrices(ctx, b.symbol, b.sizes)
}
