# Hyperliquid Go Architecture

This repo now contains a fresh Go path that copies the bot architecture, not the old exchange adapter.

Packages:

- `adapters/hyperliquid`
  - Hyperliquid REST client for public market data and metadata.
- `internal/market`
  - Venue-neutral types and interfaces.
- `internal/features`
  - Value-area and book-skew feature extraction.
- `internal/strategies`
  - Ranked long/short scanner strategy output.
- `internal/gate`
  - Score-based candidate filtering.
- `internal/risk`
  - Sizing shell for paper/live consumers.
- `internal/execution`
  - Paper execution venue.
- `internal/notify`
  - Logging notifier.
- `cmd/scan-hl`
  - Ranked long/short scanner command.
- `cmd/live-lite-hl`
  - Paper-first runtime shell on top of Hyperliquid market data.

Current status:

- Public market-data path is implemented.
- Venue-neutral interfaces are in place.
- Paper execution path is implemented.
- Live authenticated execution is intentionally stubbed and still needs signing, order semantics, and reconciliation.

Current strategy model:

- Session volume profile computes `POC`, `VAL`, and `VAH` from recent candles.
- Anchored VWAP is computed over the loaded session window and used as a regime filter.
- ADX and ATR act as regime/target filters for small-account intraday setups.
- Top-of-book skew, spread, and large visible resting levels act as microstructure quality filters.
- Scanner/live-lite currently approximate order flow from public REST `l2Book` plus candles only.
- Runtime is currently single-venue and paper-simulated in this repo.

Not implemented yet:

- `StreamL4Book` / per-order `oid` tracking.
- Address-level liquidity mapping, spoof detection, and queue-position modeling.
- Private `HypeRPC` execution, deterministic low-latency order submission, and reconciliation against live fills.
