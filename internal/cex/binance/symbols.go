package binance

import "github.com/shopspring/decimal"

// Symbol identifies a Binance market and carries the metadata needed to query
// it. Code is the wire-level identifier Binance expects (e.g. "ETHUSDC").
// EstLiquidityPerLevel is a conservative estimate of base-token units per
// orderbook level, used to choose an initial depth-endpoint tier without
// over-fetching. The estimate is per-pair because depth profiles vary
// dramatically across markets — a deep blue-chip pair like ETH-USDC has
// orders of magnitude more liquidity per level than a thin altcoin pair.
type Symbol struct {
	Code                 string
	EstLiquidityPerLevel decimal.Decimal
}

// Supported Binance markets. Add an entry here when extending the adapter
// to a new pair.
var SymbolETHUSDC = Symbol{
	Code:                 "ETHUSDC",
	EstLiquidityPerLevel: decimal.NewFromFloat(0.5),
}
