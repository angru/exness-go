# Changelog

## Unreleased — 2026-11-19

- Add tick/error wire models and shared tick/subscription/error fixtures with
  validation tests. WebSocket connection transport is not implemented yet.
- Document the shared OpenAPI checker and explicit snapshot-review workflow;
  Go runtime code and dependencies are unchanged.
- Fix discovery concurrency: queued callers honor context cancellation/deadlines,
  and `Close` no longer waits for a discovery network call. Cached reads may
  continue while an explicit refresh runs.
- Add five bounded native fuzz targets and real loopback TLS tests for cancellation,
  deadlines, truncation, redirects, request signing and connection lifecycle.
- Add instrument conditions, candle history and existing operation status,
  completing the pinned contract's read-only REST operations.
- Validate candle timeframe, price type, count/range combinations and identifier
  path segments before network calls. Preserve signed negative candle counts.
- Model numeric bid/ask/both candles and exact Decimal instrument settings;
  distinguish terminal operation states from successful execution.
- Share new fixtures with Python and document the upstream candle oneOf ambiguity.
- Add a runnable market-data example. Live interoperability remains unverified.
