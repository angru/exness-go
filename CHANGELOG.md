# Changelog

## Unreleased — 2026-11-19

- Add signed live tick streams using coder/websocket v1.8.15, with optional
  bounded reconnects, exponential backoff and explicit gap events. Cancellation,
  early iteration exit and client closure release active sockets.
- Add heartbeat, handshake deadlines, message limits and terminal error handling
  for HTTP, TLS, protocol and subscription failures. Redirects stay disabled.
- Share tick/error/subscription fixtures with Python and add synthetic TLS tests
  for signatures, reconnects, cancellation, slow readers, ping/pong and failures.
- Add a runnable read-ticks example and document stream/backpressure semantics.
- Document the shared OpenAPI checker and explicit snapshot-review workflow;
  the checker adds no Go runtime dependencies.
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
