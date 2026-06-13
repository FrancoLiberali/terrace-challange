package pipeline

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/chain"
	"github.com/FrancoLiberali/terrace-challenge/internal/dex/uniswapv3"
	"github.com/FrancoLiberali/terrace-challenge/internal/pricing"
)

// UniswapSnapshotter binds a uniswapv3.Client to a specific pool and
// trade-sizes set so it satisfies Snapshotter: one method, no
// venue-specific arguments at the call site.
//
// The block event is currently ignored — eth_call defaults to "latest"
// state, which is the just-mined block we're reacting to. Pinning the
// call to block.Number explicitly would close the (rare) race where
// block N+1 mines before our eth_call lands on the node, but
// uniswapv3.Client doesn't yet accept a block number; that's a
// refinement for later.
type UniswapSnapshotter struct {
	client *uniswapv3.Client
	pool   uniswapv3.Pool
	sizes  []decimal.Decimal
}

// NewUniswapSnapshotter wraps client to satisfy the Snapshotter interface
// with the pool and sizes bound at construction time.
func NewUniswapSnapshotter(client *uniswapv3.Client, pool uniswapv3.Pool, sizes []decimal.Decimal) *UniswapSnapshotter {
	return &UniswapSnapshotter{client: client, pool: pool, sizes: sizes}
}

// Snapshot issues the per-(size, side) QuoterV2 eth_calls in parallel.
func (u *UniswapSnapshotter) Snapshot(ctx context.Context, _ chain.BlockEvent) (pricing.Quotes, error) {
	return u.client.EffectivePrices(ctx, u.pool, u.sizes)
}
