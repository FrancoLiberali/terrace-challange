package uniswapv3

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/shopspring/decimal"

	"github.com/FrancoLiberali/terrace-challenge/internal/pricing"
	"github.com/FrancoLiberali/terrace-challenge/internal/resilience"
)

// QuoterV2Address is the deployed QuoterV2 contract on Ethereum mainnet.
var QuoterV2Address = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")

// Client wraps an Ethereum RPC client to issue QuoterV2 simulated-swap
// calls. Retries on transient JSON-RPC failures are applied per
// eth_call using the configured retry policy; rate limiting and
// circuit breaking belong to wrapper layers above this adapter.
type Client struct {
	eth    *ethclient.Client
	abi    abi.ABI
	quoter common.Address
	retry  resilience.RetryConfig
}

// NewClient dials the given Ethereum JSON-RPC endpoint and parses the
// QuoterV2 ABI once for reuse across calls. Each eth_call is issued
// without retry — suitable for tests and probe binaries; arbd uses
// NewClientWithRetry to attach a backoff policy.
func NewClient(rpcURL string) (*Client, error) {
	return NewClientWithRetry(rpcURL, resilience.RetryConfig{})
}

// NewClientWithRetry is NewClient with a configurable per-eth_call
// retry policy. Transient JSON-RPC failures (network errors, server
// 5xx) are retried with exponential backoff and jitter; deterministic
// failures ("execution reverted" and the like) are surfaced
// immediately so the per-row Quote.Err captures the actual contract
// outcome.
func NewClientWithRetry(rpcURL string, retry resilience.RetryConfig) (*Client, error) {
	eth, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial RPC: %w", err)
	}
	parsed, err := abi.JSON(strings.NewReader(quoterV2ABI))
	if err != nil {
		eth.Close()
		return nil, fmt.Errorf("parse QuoterV2 ABI: %w", err)
	}
	return &Client{eth: eth, abi: parsed, quoter: QuoterV2Address, retry: retry}, nil
}

// Close releases the underlying RPC connection.
func (c *Client) Close() { c.eth.Close() }

// EffectivePrices returns the slippage-aware effective per-unit price for
// each (size, side) combination against the given pool's current state.
//
// Buy[i] and Sell[i] in the returned Quotes both refer to sizes[i]:
//   - Buy[i] simulates spending Quote token to receive exactly sizes[i] of
//     Base, via QuoterV2.quoteExactOutputSingle. Price = amountIn / size.
//   - Sell[i] simulates sending exactly sizes[i] of Base and receiving
//     Quote, via QuoterV2.quoteExactInputSingle. Price = amountOut / size.
//
// Per-row failures (e.g., the pool reverting because it cannot service the
// requested size) are recorded in Quote.Err; the top-level error is
// returned only if the input cannot be processed at all.
func (c *Client) EffectivePrices(ctx context.Context, pool Pool, sizes []decimal.Decimal) (pricing.Quotes, error) {
	out := pricing.Quotes{
		Buy:  make([]pricing.Quote, len(sizes)),
		Sell: make([]pricing.Quote, len(sizes)),
	}
	// Each call is independent and each goroutine writes to a unique slice
	// slot, so no synchronization is needed beyond the WaitGroup. HTTP/2
	// multiplexes the 2N concurrent eth_calls over a single TCP connection
	// to the RPC endpoint.
	var wg sync.WaitGroup
	for i, size := range sizes {
		wg.Go(func() { out.Buy[i] = c.quoteBuy(ctx, pool, size) })
		wg.Go(func() { out.Sell[i] = c.quoteSell(ctx, pool, size) })
	}
	wg.Wait()
	return out, nil
}

// exactInputParams matches QuoterV2.quoteExactInputSingle's tuple input.
type exactInputParams struct {
	TokenIn           common.Address `abi:"tokenIn"`
	TokenOut          common.Address `abi:"tokenOut"`
	AmountIn          *big.Int       `abi:"amountIn"`
	Fee               *big.Int       `abi:"fee"`
	SqrtPriceLimitX96 *big.Int       `abi:"sqrtPriceLimitX96"`
}

// exactOutputParams matches QuoterV2.quoteExactOutputSingle's tuple input.
type exactOutputParams struct {
	TokenIn           common.Address `abi:"tokenIn"`
	TokenOut          common.Address `abi:"tokenOut"`
	Amount            *big.Int       `abi:"amount"`
	Fee               *big.Int       `abi:"fee"`
	SqrtPriceLimitX96 *big.Int       `abi:"sqrtPriceLimitX96"`
}

// quoteSell simulates sending `size` Base units and computes the
// effective per-unit Quote price from the resulting amountOut.
func (c *Client) quoteSell(ctx context.Context, pool Pool, size decimal.Decimal) pricing.Quote {
	return c.quote(ctx, pricing.Sell, size, pool.Quote.Decimals, "quoteExactInputSingle", exactInputParams{
		TokenIn:           pool.Base.Address,
		TokenOut:          pool.Quote.Address,
		AmountIn:          toRawAmount(size, pool.Base.Decimals),
		Fee:               big.NewInt(int64(pool.Fee)),
		SqrtPriceLimitX96: new(big.Int),
	})
}

// quoteBuy simulates the Quote token cost of receiving exactly `size`
// Base units and computes the effective per-unit Quote price from the
// resulting amountIn.
func (c *Client) quoteBuy(ctx context.Context, pool Pool, size decimal.Decimal) pricing.Quote {
	return c.quote(ctx, pricing.Buy, size, pool.Quote.Decimals, "quoteExactOutputSingle", exactOutputParams{
		TokenIn:           pool.Quote.Address,
		TokenOut:          pool.Base.Address,
		Amount:            toRawAmount(size, pool.Base.Decimals),
		Fee:               big.NewInt(int64(pool.Fee)),
		SqrtPriceLimitX96: new(big.Int),
	})
}

// quote shares the call → unpack → price-math path between Buy and Sell.
// QuoterV2's two functions return the load-bearing value (amountOut for
// exactInput, amountIn for exactOutput) in the same first output slot, so
// both directions reduce to "first big.Int divided by size, denominated
// in quote decimals." The 4th slot carries a per-call gas estimate — the
// number of gas units QuoterV2 would charge if this exact swap were
// executed against the current pool state — which we surface verbatim
// on the Quote.
func (c *Client) quote(ctx context.Context, side pricing.Side, size decimal.Decimal, quoteDecimals uint8, method string, params any) pricing.Quote {
	raw, err := c.call(ctx, method, params)
	if err != nil {
		return pricing.Quote{Size: size, Side: side, Err: err}
	}
	primary, ok := raw[0].(*big.Int)
	if !ok {
		return pricing.Quote{Size: size, Side: side, Err: fmt.Errorf("unexpected primary output type %T", raw[0])}
	}
	// raw[3] is gasEstimate (uint256 in solidity; *big.Int in Go).
	// uint256 → uint64 conversion is safe in practice: an Ethereum
	// transaction's gas limit is bounded by the block gas limit
	// (~30M units today), so any single-swap estimate fits in uint64
	// with ~12 orders of magnitude to spare.
	gasBig, ok := raw[3].(*big.Int)
	if !ok {
		return pricing.Quote{Size: size, Side: side, Err: fmt.Errorf("unexpected gasEstimate output type %T", raw[3])}
	}
	if !gasBig.IsUint64() {
		return pricing.Quote{Size: size, Side: side, Err: fmt.Errorf("gasEstimate overflows uint64: %s", gasBig)}
	}
	price := fromRawAmount(primary, quoteDecimals).Div(size)
	return pricing.Quote{
		Size:        size,
		Side:        side,
		Price:       price,
		GasEstimate: gasBig.Uint64(),
	}
}

// call packs the given method's params, fires an eth_call to QuoterV2 at
// latest state, and returns the unpacked outputs. Transient JSON-RPC
// failures are retried per the Client's retry policy; deterministic
// failures (contract reverts, ABI mismatches) are surfaced after the
// first attempt.
func (c *Client) call(ctx context.Context, method string, params any) ([]any, error) {
	data, err := c.abi.Pack(method, params)
	if err != nil {
		return nil, fmt.Errorf("pack %s: %w", method, err)
	}
	var raw []byte
	op := func() error {
		var callErr error
		raw, callErr = c.eth.CallContract(ctx, ethereum.CallMsg{To: &c.quoter, Data: data}, nil)
		if callErr != nil && isDeterministicCallErr(callErr) {
			return resilience.Permanent(callErr)
		}
		return callErr
	}
	if err = resilience.Retry(ctx, op, c.retry); err != nil {
		return nil, fmt.Errorf("eth_call %s: %w", method, err)
	}
	out, err := c.abi.Unpack(method, raw)
	if err != nil {
		return nil, fmt.Errorf("unpack %s: %w", method, err)
	}
	return out, nil
}

// isDeterministicCallErr reports whether err is a contract-side
// outcome that cannot be remedied by retrying (e.g., the pool reverts
// because it cannot service the requested size). The classification
// is string-based because go-ethereum surfaces JSON-RPC errors as
// plain error values across nodes/clients — there is no portable
// typed error to assert on.
func isDeterministicCallErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range deterministicCallErrMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// deterministicCallErrMarkers are substrings that, when present in a
// CallContract error message, identify the failure as a contract-side
// outcome rather than a transient transport blip. "execution reverted"
// is the standard EVM revert string; the others cover the canonical
// deterministic-failure shapes Geth and major providers emit.
var deterministicCallErrMarkers = []string{
	"execution reverted",
	"invalid opcode",
	"out of gas",
	"insufficient funds",
}
