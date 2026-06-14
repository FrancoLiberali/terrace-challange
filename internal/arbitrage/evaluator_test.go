package arbitrage

import (
	"math/big"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/chain"
	"github.com/FrancoLiberali/terrace-challenge/internal/pathfinder"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// defaultGasUnits is the per-swap gas estimate the tests use unless they
// override it explicitly. Matches the historic rule-of-thumb value so
// the expected gas/net numbers stay comparable to prior tests.
const defaultGasUnits uint64 = 150_000

// mkPath builds a synthetic CEX→DEX candidate path with a default gas
// estimate. baseFee is in gwei for readability; we convert to wei here.
// Venues are fixed to ("binance", "uniswap") because the evaluator's
// behavior does not depend on venue identity any more (trading fees
// are folded into BuyPrice/SellPrice by the adapters upstream).
func mkPath(size, buyPrice, sellPrice string, baseFeeGwei int64) pathfinder.CandidatePath {
	return mkPathGas(size, buyPrice, sellPrice, baseFeeGwei, defaultGasUnits)
}

// mkPathGas is mkPath with the gas estimate overridable. Used by tests
// that vary gas explicitly.
func mkPathGas(size, buyPrice, sellPrice string, baseFeeGwei int64, gasUnits uint64) pathfinder.CandidatePath {
	const gweiInWei = 1_000_000_000
	baseFeeWei := new(big.Int).Mul(big.NewInt(baseFeeGwei), big.NewInt(gweiInWei))
	return pathfinder.CandidatePath{
		Block: chain.BlockEvent{
			Number:    100,
			Timestamp: time.Unix(1_700_000_000, 0).UTC(),
			BaseFee:   baseFeeWei,
		},
		Size:        dec(size),
		BuyVenue:    "binance",
		SellVenue:   "uniswap",
		BuyPrice:    dec(buyPrice),
		SellPrice:   dec(sellPrice),
		GasEstimate: gasUnits,
	}
}

// standardModel: $1 minimum profit threshold. Trading fees are no longer
// in the model — each adapter folds its venue's fees into the prices it
// returns (see architecture.md decision 3), so the evaluator only adds
// gas. Tests therefore feed prices that are already post-fee.
func standardModel() CostModel {
	return CostModel{
		MinNetProfitUSDC: decimal.NewFromInt(1),
	}
}

func TestEvaluator_ProfitableArbAcrossVenues(t *testing.T) {
	// Buy 10 ETH on Binance at $1681.68 (= raw $1680 + 10 bps taker fee
	// already folded in by the adapter); sell on Uniswap at $1690 (the
	// 0.3% pool fee was already folded in by QuoterV2). The Evaluator's
	// only job is to subtract gas.
	//
	// Spread × Size: (1690 - 1681.68) × 10 = 83.20
	// Gas: 150k × 1 gwei × 10^-18 × 1681.68 = 0.252252
	// Net: 83.20 - 0.252252 = 82.947748
	e := NewEvaluator(standardModel())
	op := e.Evaluate(mkPath("10", "1681.68", "1690", 1))

	if !op.GrossProfit.Equal(dec("83.20")) {
		t.Errorf("gross: got %s, want 83.20", op.GrossProfit)
	}
	if !op.GasCostUSDC.Equal(dec("0.252252")) {
		t.Errorf("gas: got %s, want 0.252252", op.GasCostUSDC)
	}
	if !op.NetProfit.Equal(dec("82.947748")) {
		t.Errorf("net: got %s, want 82.947748", op.NetProfit)
	}
	if !op.CapitalUSDC.Equal(dec("16816.80")) {
		t.Errorf("capital: got %s, want 16816.80", op.CapitalUSDC)
	}
	if !e.IsProfitable(op) {
		t.Error("opportunity should be profitable at $1 threshold")
	}
}

func TestEvaluator_LossMakingNotProfitable(t *testing.T) {
	// Buy at 1685, sell at 1680 — gross is negative. Even before
	// fees and gas this isn't profitable.
	e := NewEvaluator(standardModel())
	op := e.Evaluate(mkPath("1", "1685", "1680", 1))

	if !op.SpreadPerUnit.Equal(dec("-5")) {
		t.Errorf("spread: got %s, want -5", op.SpreadPerUnit)
	}
	if op.NetProfit.IsPositive() {
		t.Errorf("net should be negative, got %s", op.NetProfit)
	}
	if e.IsProfitable(op) {
		t.Error("opportunity should not be profitable")
	}
}

func TestEvaluator_GasCostScalesWithBaseFee(t *testing.T) {
	// Compare gas cost at baseFee=10 gwei vs 1 gwei: should be 10× higher.
	e := NewEvaluator(standardModel())
	lo := e.Evaluate(mkPath("1", "1680", "1690", 1))
	hi := e.Evaluate(mkPath("1", "1680", "1690", 10))

	ratio := hi.GasCostUSDC.Div(lo.GasCostUSDC)
	if !ratio.Round(4).Equal(dec("10")) {
		t.Errorf("gas ratio (10 gwei vs 1 gwei): got %s, want 10", ratio.Round(4))
	}
}

func TestEvaluator_NilBaseFeeTreatedAsZero(t *testing.T) {
	// Defensive: a candidate carrying a nil BaseFee (pre-London chain?
	// fixture mistake?) should not panic.
	path := mkPath("1", "1680", "1690", 1)
	path.Block.BaseFee = nil

	e := NewEvaluator(standardModel())
	op := e.Evaluate(path)
	if !op.GasCostUSDC.Equal(decimal.Zero) {
		t.Errorf("nil baseFee should produce zero gas cost, got %s", op.GasCostUSDC)
	}
}

func TestEvaluator_NetProfitPctOnZeroCapital(t *testing.T) {
	// Defensive: zero buy price → zero capital → pct division by zero.
	// Evaluator must return 0 pct rather than panic or NaN.
	e := NewEvaluator(standardModel())
	op := e.Evaluate(mkPath("1", "0", "10", 0))
	if !op.NetProfitPct.Equal(decimal.Zero) {
		t.Errorf("expected 0 pct on zero capital, got %s", op.NetProfitPct)
	}
}

func TestEvaluator_IsProfitableHonorsThreshold(t *testing.T) {
	// Net profit = $10 - $1.68 - $0.252 = $8.068. With a $5 threshold,
	// profitable. With a $10 threshold, not.
	path := mkPath("1", "1680", "1690", 1)

	above := standardModel()
	above.MinNetProfitUSDC = decimal.NewFromInt(5)
	if !NewEvaluator(above).IsProfitable(NewEvaluator(above).Evaluate(path)) {
		t.Error("should be profitable at $5 threshold")
	}

	below := standardModel()
	below.MinNetProfitUSDC = decimal.NewFromInt(10)
	if NewEvaluator(below).IsProfitable(NewEvaluator(below).Evaluate(path)) {
		t.Error("should NOT be profitable at $10 threshold")
	}
}
