// Package arbitrage applies a cost model to CandidatePaths produced by
// the Pathfinder and decides whether each one is profitable. Evaluation
// is a pure function over the path and the configured CostModel — no
// I/O, no goroutines.
package arbitrage

import (
	"math/big"

	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/pathfinder"
)

// CostModel parameterises the profitability calculation. Trading fees do
// not appear here: each adapter folds its venue's intrinsic fees into
// the Price it returns (see architecture.md decision 3), so the evaluator
// only adds the costs that are NOT venue-intrinsic — currently just gas.
// Gas units travel on each CandidatePath rather than the model.
type CostModel struct {
	// MinNetProfitUSDC is the threshold IsProfitable compares NetProfit
	// against. Decimal so callers can set fractional thresholds.
	MinNetProfitUSDC decimal.Decimal
}

// Opportunity is the result of evaluating a CandidatePath against the
// cost model. CandidatePath is embedded so callers see the underlying
// venues, prices, and block context.
//
// Profit fields are USDC-denominated. NetProfitPct is the net profit
// expressed as a percentage of the capital required.
type Opportunity struct {
	pathfinder.CandidatePath

	SpreadPerUnit decimal.Decimal // SellPrice - BuyPrice (both post-fee)
	GrossProfit   decimal.Decimal // SpreadPerUnit × Size — already net of venue-intrinsic fees, gross of gas
	GasCostUSDC   decimal.Decimal // gas estimate valued in USDC
	NetProfit     decimal.Decimal // GrossProfit - GasCostUSDC
	NetProfitPct  decimal.Decimal // (NetProfit / CapitalUSDC) × 100
	CapitalUSDC   decimal.Decimal // BuyPrice × Size — what you need to put up
}

// Evaluator holds the cost model and applies it to candidate paths.
type Evaluator struct {
	model CostModel
}

// NewEvaluator returns an Evaluator using the given cost model.
func NewEvaluator(model CostModel) *Evaluator {
	return &Evaluator{model: model}
}

// weiToETHShift is the number of decimal places between wei and ETH.
const weiToETHShift = 18

// Evaluate computes the Opportunity for the candidate, regardless of
// profitability. Callers use IsProfitable to filter.
//
// Trading fees are NOT subtracted here — they are already baked into the
// candidate's BuyPrice and SellPrice by the per-venue adapters (see
// pricing.Quote and architecture.md decision 3). The only cost the
// evaluator adds is gas, which is per-transaction and therefore cannot
// live on the per-unit price.
func (e *Evaluator) Evaluate(path pathfinder.CandidatePath) Opportunity {
	spreadPerUnit := path.SellPrice.Sub(path.BuyPrice)
	grossProfit := spreadPerUnit.Mul(path.Size)
	capital := path.BuyPrice.Mul(path.Size)

	// Gas cost: gasUnits × baseFee → wei → ETH → USDC (valued at
	// BuyPrice as a reasonable per-block ETH→USDC reference).
	// gasUnits comes from the candidate itself: the adapter populates
	// it per-quote (QuoterV2 returns one), and the Pathfinder sums the
	// two legs into CandidatePath.GasEstimate.
	gasUSDC := gasCostUSDC(path.GasEstimate, path.Block.BaseFee, path.BuyPrice)

	netProfit := grossProfit.Sub(gasUSDC)

	netProfitPct := decimal.Zero
	if capital.IsPositive() {
		netProfitPct = netProfit.Div(capital).Mul(decimal.NewFromInt(100)) //nolint:mnd // percentage scale
	}

	return Opportunity{
		CandidatePath: path,
		SpreadPerUnit: spreadPerUnit,
		GrossProfit:   grossProfit,
		GasCostUSDC:   gasUSDC,
		NetProfit:     netProfit,
		NetProfitPct:  netProfitPct,
		CapitalUSDC:   capital,
	}
}

// IsProfitable reports whether the opportunity's net profit strictly
// exceeds the configured threshold.
func (e *Evaluator) IsProfitable(o Opportunity) bool {
	return o.NetProfit.GreaterThan(e.model.MinNetProfitUSDC)
}

// gasCostUSDC converts gasUnits × baseFee (wei) to USDC using the given
// ETH-in-USDC price as the reference. BaseFee is the EIP-1559 minimum;
// this systematically underestimates the true cost because it ignores
// the priority fee (see limitations.md §7).
//
// Returns zero when gasUnits is 0 (off-chain venue path) or baseFee is
// nil (defensive — should not happen on post-London mainnet).
func gasCostUSDC(gasUnits uint64, baseFeeWei *big.Int, ethPrice decimal.Decimal) decimal.Decimal {
	if baseFeeWei == nil || gasUnits == 0 {
		return decimal.Zero
	}
	gasWei := new(big.Int).Mul(new(big.Int).SetUint64(gasUnits), baseFeeWei)
	gasETH := decimal.NewFromBigInt(gasWei, 0).Shift(-int32(weiToETHShift))
	return gasETH.Mul(ethPrice)
}
