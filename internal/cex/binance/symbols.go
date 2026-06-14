package binance

import "github.com/shopspring/decimal"

// Symbol identifies a Binance market and carries the per-market metadata
// the adapter uses to query it and to produce fee-adjusted prices.
//
// Code is the wire-level identifier Binance expects (e.g. "ETHUSDC").
//
// EstLiquidityPerLevel is a conservative estimate of base-token units
// per orderbook level, used to choose an initial depth-endpoint tier
// without over-fetching. The estimate is per-pair because depth
// profiles vary dramatically across markets — a deep blue-chip pair
// like ETH-USDC has orders of magnitude more liquidity per level than
// a thin altcoin pair.
//
// TakerFeeBps is the venue's taker fee for this market in basis points
// (10 = 0.1%). The adapter folds it into the effective per-unit price
// it returns, so downstream consumers see a quote already net of fees
// — see architecture.md decision 3.
type Symbol struct {
	Code                 string
	EstLiquidityPerLevel decimal.Decimal
	TakerFeeBps          uint32
}

// ethusdcLiquidityPerLevel is a deliberately conservative estimate of
// base-token units per orderbook level for ETH-USDC: top-of-book often
// shows tens of ETH per level, but the tail thins out quickly. At 5
// ETH/level the depth-tier heuristic picks the cheapest tier (weight=5)
// for the configured trade sizes and only escalates when the book is
// genuinely thin.
const ethusdcLiquidityPerLevel int64 = 5

// binanceSpotTakerFeeBps is the standard taker fee on Binance Spot
// markets (0.1%). Different markets — or BNB-discounted accounts —
// could carry a different value, which is why this lives per-Symbol
// rather than as a package constant.
const binanceSpotTakerFeeBps uint32 = 10

// SymbolETHUSDC is the ETH-USDC market on Binance Spot.
var SymbolETHUSDC = Symbol{
	Code:                 "ETHUSDC",
	EstLiquidityPerLevel: decimal.NewFromInt(ethusdcLiquidityPerLevel),
	TakerFeeBps:          binanceSpotTakerFeeBps,
}
