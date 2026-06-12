# terrace-challange

Real-time CEX-DEX arbitrage detection between Binance and Uniswap V3 for the ETH-USDC pair, sampled on every Ethereum block. Built in Go as part of the Terrace Senior Software Engineer coding challenge.

## Documentation

- [**CHALLENGE.md**](./CHALLENGE.md) — the original challenge specification (converted from the provided PDF) describing requirements, deliverables, and evaluation criteria.
- [**business.md**](./business.md) — business context: what the system detects, why CEX and DEX prices diverge, and why the Ethereum block is the natural clock for the design.
- [**limitations.md**](./limitations.md) — explicit list of known limitations, risks, and missed opportunities of the simplified detection-only design, plus what a production trading extension would require.
