# Implementation <!-- omit from toc -->

This document maps the architecture described in [`architecture.md`](./architecture.md) to its Go-level structure: package layout, interface seam locations, and code-level conventions. It is the implementation-detail counterpart to architecture.md and will grow as the code lands.

For the conceptual architecture (what components exist, how they relate, the design decisions and trade-offs), see [`architecture.md`](./architecture.md). For business context see [`business.md`](./business.md), and for known limitations see [`limitations.md`](./limitations.md).

---

## Table of Contents <!-- omit from toc -->

- [Package layout](#package-layout)
- [Conventions](#conventions)
- [Interface seams in code](#interface-seams-in-code)
- [Resilience composition pattern](#resilience-composition-pattern)
- [Numeric types for financial math](#numeric-types-for-financial-math)

---

## Package layout

```
terrace-challenge/
├── cmd/
│   ├── arbd/main.go                  # wire components, start runtime
│   ├── probe-binance/main.go         # diagnostic: Binance effective prices
│   ├── probe-uniswap/main.go         # diagnostic: QuoterV2 effective prices
│   └── probe-chain/main.go           # diagnostic: WS newHeads + reconnect
├── internal/                         # not importable by external code
│   ├── chain/
│   │   └── subscriber.go             # WS newHeads + reconnect; BlockEvent type
│   ├── cex/binance/
│   │   ├── client.go                 # HTTP client; EffectivePrices
│   │   ├── orderbook.go              # walk book → fee-adjusted price (pure)
│   │   └── symbols.go                # Symbol metadata, taker fee
│   ├── dex/uniswapv3/
│   │   ├── client.go                 # eth_call wrapper; EffectivePrices
│   │   ├── abi.go                    # QuoterV2 ABI
│   │   ├── decimals.go               # raw ↔ human amount conversion
│   │   └── tokens.go                 # Token / Pool metadata
│   ├── pricing/quote.go              # unified Quote / Quotes types
│   ├── pipeline/
│   │   ├── dispatcher.go             # per-block fan-out, Snapshotter iface
│   │   ├── binance.go                # Binance Snapshotter implementation
│   │   └── uniswap.go                # Uniswap Snapshotter implementation
│   ├── pathfinder/pathfinder.go      # candidate-path enumeration (pure)
│   ├── arbitrage/evaluator.go        # cost model → Opportunity (pure)
│   └── resilience/
│       ├── ratelimit.go              # token bucket (wraps x/time/rate)
│       ├── breaker.go                # circuit breaker (wraps gobreaker)
│       └── retry.go                  # NewHTTPClient + transport wrappers
├── go.mod
└── (docs at repo root)
```

Config loading and a YAML format are deferred to Step 7. Alert output
lives directly in `cmd/arbd/main.go` (`emitOpportunity` →
structured `slog.Info` + optional pretty block) — an injectable
`OpportunitySink` interface would be premature for a single output.

---

## Conventions

- **`cmd/arbd/`** is the only package that knows how the pieces fit together. Everything else is a library that knows nothing about how it is wired. This makes alternative configurations easy and tests trivial.
- **`internal/`** keeps the application code unimportable from outside the module. Standard Go convention for application code; prevents accidental coupling if the repo grows.
- **Adapters live in subdirectories named after the venue** (`cex/binance/`, `dex/uniswapv3/`). Adding a second CEX is `cex/coinbase/`; adding a second DEX is `dex/sushiswapv3/`. The Snapshotter interface that unifies them lives in `pipeline/`, which is the only consumer.
- **`pricing/`, `pathfinder/`, and `arbitrage/` are pure packages** — no network, no I/O, no goroutines. They are the unit-test sweet spot. `pricing/` is imported by the adapter packages so that adapters produce the unified effective-price shape directly. `pathfinder/` and `arbitrage/` are downstream pipeline stages: the first finds candidate paths, the second evaluates their profitability.
- **`resilience/` is generic** — nothing in it knows about CEX vs DEX. It exposes a `NewHTTPClient` factory that the adapters' HTTP clients use; the resilience layers live at the HTTP transport, one stack per external host.

---

## Interface seams in code

The implementation exposes one explicit unification seam plus two configuration seams. Everything else stayed concrete — abstracting it would add noise without enabling real flexibility.

| Seam | Package | Purpose |
|---|---|---|
| `Snapshotter` | `internal/pipeline` | One method `Snapshot(ctx, BlockEvent) (Quotes, error)`. The single shape every venue produces, regardless of whether it's CEX, DEX, or future market structure. Downstream code (Dispatcher, Pathfinder, Evaluator) does not care which venue any given price came from. |
| `*http.Client` (via `resilience.NewHTTPClient`) | `internal/resilience` | The composed resilience stack per host. The `*http.Client` shape is stdlib so adapter code stays unaware of which libraries back the retry / breaker / rate-limit layers. |
| `BreakerConfig.OnStateChange` | `internal/resilience` | Callback fired on every breaker state transition. `cmd/arbd` hooks it to a `slog.Warn` so operator visibility is wired without the breaker package depending on slog. |

`BinanceSnapshotter` and `UniswapSnapshotter` are concrete implementations of `Snapshotter` that bind their venue-specific configuration at construction time. The Dispatcher holds a `map[string]Snapshotter` — adding a venue is a `pipeline.NewXxxSnapshotter` + a map entry in `main.go`.

Internal pipeline components (Dispatcher, Pathfinder, Profitability Evaluator) are intentionally concrete structs and pure functions, not interfaces — abstracting them would add noise without enabling any real flexibility.

---

## Resilience composition pattern

Architecture decision 6 (in [`architecture.md`](./architecture.md#6-resilience-is-wrapped-not-embedded)) requires resilience concerns — rate limiting, circuit breaking, retries — to be applied as middleware around external dependencies, not embedded inside each adapter. The implementation uses the **decorator pattern at the HTTP transport layer**: each concern is an `http.RoundTripper` (or surrounding helper) that wraps the next, composed into a single `*http.Client` returned by `resilience.NewHTTPClient`. Each adapter accepts that `*http.Client` and uses it for every outbound call.

### Four-layer structure (per host)

```
┌─────────────────────────────────────────────────┐
│   retry  (hashicorp/go-retryablehttp)           │
│   ┌──────────────────────────────────────────┐  │
│   │ circuit breaker  (sony/gobreaker)        │  │
│   │   ┌────────────────────────────────────┐ │  │
│   │   │ rate limit  (golang.org/x/time/rate)│ │  │
│   │   │   ┌──────────────────────────────┐ │ │  │
│   │   │   │ real http.Transport          │ │ │  │
│   │   │   └──────────────────────────────┘ │ │  │
│   │   └────────────────────────────────────┘ │  │
│   └──────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
```

One stack per external host. Binance and the Uniswap RPC provider each get their own breaker, rate budget, and retry policy — failures in one venue cannot trip the other's breaker, and partial results from healthy endpoints in a future multi-endpoint Snapshotter survive.

### Why HTTP-transport layer, not the Snapshotter

An earlier iteration of this implementation wrapped Snapshotters with `RateLimited(Snapshotter)` + `CircuitBroken(Snapshotter)` decorators. Two problems with that shape:

- **Rate limit was misaligned with the venue's actual quota.** A Snapshotter wraps a logical "fetch quotes for these sizes" operation, which fires multiple HTTP calls underneath. With retries inside the raw client, one Snapshot that retried 4 times still consumed only 1 rate token — so the effective HTTP rate during failure bursts could be up to 5× the nominal budget.
- **Breaker granularity was wrong for partial results.** A Snapshotter that internally fetches from multiple endpoints (multi-pool DEX queries, multi-RPC failover, multi-CEX aggregation in a future extension) should be able to return partial results when only some endpoints are sick. A Snapshot-level breaker would fail the whole Snapshot on one bad endpoint.

Moving the resilience layers to per-host HTTP transport fixes both: each HTTP call is exactly one rate token, and each external dependency has its own breaker.

### Stacking rationale

- **Retry outermost** so it sees the final outcome of each attempt (real success, transient blip, breaker rejection) and decides whether to keep going.
- **Breaker inside retry** so its `ErrOpenState` surfaces to the retry layer, where `CheckRetry` classifies it as permanent and aborts. Retries don't burn budget on a known-open breaker.
- **Rate limit innermost** so it gates actual network I/O; calls rejected by the breaker never consume a token.
- **Real transport** innermost does the work.

### Classification details

- The breaker transport translates 5xx HTTP responses into breaker-counted failures. Go's HTTP semantics return `(resp_500, nil)` for 5xx, so without this a server consistently returning 500 would never trip the breaker. The 5xx marker error is swallowed on the way out so the response itself still flows upstream — retryablehttp's policy sees the status code and decides whether to retry.
- `IsSuccessful` skips `context.Canceled` (caller decision, e.g., SIGTERM mid-call — nothing about the dependency went wrong) but counts `context.DeadlineExceeded` (provider didn't respond within our configured timeout — that IS a health signal).
- `CheckRetry` recognises `gobreaker.ErrOpenState` and returns `(false, err)` immediately, propagating the breaker error upstream without further attempts.

### Code shape

```go
// Per-host: build one *http.Client with the four layers composed.
binanceHTTP := resilience.NewHTTPClient(resilience.HTTPClientConfig{
    Retry:          resilience.DefaultRetryConfig(),
    Limiter:        resilience.NewRateLimiter("binance", 5, 2),
    Breaker:        resilience.NewCircuitBreaker(resilience.BreakerConfig{
        Name:         "binance",
        MinRequests:  20,
        FailureRatio: 0.2,
        Cooldown:     30 * time.Second,
        Interval:     time.Minute,
        OnStateChange: breakerStateLog, // hook to slog
    }),
    RequestTimeout: 10 * time.Second,
    Logger:         slog.Default(),
})
binanceClient := binance.NewClientWithHTTP(binance.DefaultBaseURL, binanceHTTP)

// Uniswap mirrors the shape. The DEX client routes eth_calls through
// the resilience-equipped *http.Client via
//   rpc.DialOptions(ctx, url, rpc.WithHTTPClient(c)) + ethclient.NewClient(rpc)
// so every JSON-RPC request inherits the four-layer stack.
```

The Snapshotter implementations (`pipeline.BinanceSnapshotter`, `pipeline.UniswapSnapshotter`) are unaware of the resilience layers — they just call the raw client whose HTTP transport carries them.

### Why this shape

- **Generic and library-agnostic from the adapter's POV.** Both adapters depend only on `*http.Client`. Swapping the underlying breaker library is a change inside `internal/resilience/` with no ripple.
- **Each layer is independently testable.** The breaker has its own state-transition tests; the retry transport is exercised by an `httptest` server that 503s twice then succeeds; the rate limit gates by elapsed time.
- **Composable per call site.** `HTTPClientConfig` fields are optional — pass nil for any layer to skip it. Tests construct clients with just `Retry` (no breaker, no limiter); production wires all four.
- **Reusable across venues.** The same factory works for any HTTP-speaking dependency; the Uniswap JSON-RPC client uses it via `rpc.WithHTTPClient`.

### What stays outside this stack

- **Per-call timeouts** apply via `HTTPClientConfig.RequestTimeout` (per attempt, not per total retry budget — a single hung connection cannot consume the whole window) and the caller's `context` deadline.
- **Structured request logging** flows through retryablehttp's `LeveledLogger` interface, adapted to slog inside `resilience.NewHTTPClient`. Operator-facing logs (breaker state changes, rate-limit waits, breaker rejections) live in the wrapper types and emit through slog directly.

---

## Numeric types for financial math

Architecture decision 8 (in [`architecture.md`](./architecture.md#8-precise-decimal-arithmetic-for-prices)) requires exact decimal arithmetic for all prices and amounts. The concrete type used is `shopspring/decimal.Decimal`.

Rationale for this specific library:

- `float64` is rejected outright: precision errors compound through walk-the-book calculations and produce phantom or missed arbitrage detections at the margin.
- `big.Float` from the standard library would be correct but its API is more cumbersome for arithmetic-heavy code.
- `shopspring/decimal` is the standard choice for financial code in Go, listed in the challenge's recommended libraries, and provides an arithmetic-friendly API.

Raw amounts from external systems are normalized to `decimal.Decimal` at the adapter boundary; nothing downstream sees the native representations.
