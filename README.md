# terrace-challenge

Real-time CEX-DEX arbitrage detection between Binance and Uniswap V3 for the ETH-USDC pair, sampled on every Ethereum block. Built in Go as part of the Terrace Senior Software Engineer coding challenge.

## Configuration

Local configuration lives in a `.env` file at the repo root. `.env` is gitignored; the committed `.env.example` is the template.

```bash
cp .env.example .env
# edit .env and fill in your Alchemy URLs
```

Every binary that needs configuration loads `.env` automatically on startup (via [godotenv](https://github.com/joho/godotenv)). Missing `.env` is not an error — values found in the real process environment always win, so `.env` is for local convenience and CI / production just sets the variables directly.

```bash
go run ./cmd/probe-binance   # no config required
go run ./cmd/probe-uniswap   # auto-loads .env, reads ETH_RPC_URL
```

### Required environment variables

| Variable | Used by | How to get it |
|---|---|---|
| `ETH_RPC_URL` | `cmd/probe-uniswap` (Step 2 and beyond) | Free Alchemy app on Ethereum Mainnet → "Endpoints" → HTTPS URL |
| `ETH_RPC_WS_URL` | `cmd/probe-chain` (Step 3 and beyond) | Same Alchemy app → "Endpoints" → WSS URL |

`cmd/probe-binance` needs no credentials — Binance's public REST endpoint is open.

## Documentation

- [**CHALLENGE.md**](./CHALLENGE.md) — the original challenge specification (converted from the provided PDF) describing requirements, deliverables, and evaluation criteria.
- [**business.md**](./business.md) — business context: what the system detects, why CEX and DEX prices diverge, and why the Ethereum block is the natural clock for the design.
- [**architecture.md**](./architecture.md) — conceptual architecture: components, data flow, design decisions, trade-offs, and what a production-scale version would look like.
- [**implementation.md**](./implementation.md) — Go-level structure: package layout, interface seams in code, and conventions.
- [**plan.md**](./plan.md) — step-by-step implementation plan: integration-first, types-on-demand, with verification per step.
- [**limitations.md**](./limitations.md) — explicit list of known limitations, risks, and missed opportunities of the simplified detection-only design, plus what a production trading extension would require.
