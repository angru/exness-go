# Changelog

## Unreleased

- Implement all five trading submission methods with explicit idempotency keys,
  exact signed bodies/queries and no automatic write retries.
- Add limit/stop/SLTP, modification and closure request types; preserve PATCH
  omission versus null and validate close-by zero-volume semantics before networking.
- Report lost/invalid ACKs and ambiguous HTTP outcomes as UnknownOutcomeError;
  preserve cancellation semantics and distinguish preflight/discovery failures.
- Verify shared write scenarios, real TLS bytes, rejection, redirect, truncation,
  timeout and cancellation boundaries; live trading remains unverified.

- Document the staged trading-write plan and implement its first foundation:
  validated opening-position/ACK models and exact-body mutation signing with
  explicit idempotency keys. Client submission methods remain planned.
- Share OpenSSL-generated POST/PATCH/DELETE signing vectors with Python and verify
  signed method, path/query and body bytes over local TLS, including Unicode.

## v0.1.0-alpha.7 — 2026-11-19

- Review and document the public API and cross-language lifecycle/validation limits.
- Add neutral StreamOptions/StreamGap aliases with full legacy-name compatibility.
- Exercise all public stream adapters from an external package, including typed
  options and cancellation errors. Prepare the first tagged alpha release.

- Add typed account-state, instrument and HMR streams over the existing signed
  transport, with copied/validated selections and opt-in reconnect gaps.
- Require fresh HMR snapshots on each connection, preserve schedule strings and
  distinguish full instrument lifecycle updates from HMR incremental changes.
- Add shared Python fixtures, local TLS recovery/cleanup tests, a read-events
  example and explicit cache recovery guidance. Live interoperability is unverified.

- Add signed transactions streaming with typed snapshot/delta/result models and
  a fresh-snapshot requirement on every connection. Share the tick transport and
  its bounded reconnects, gap reporting and cancellation behavior.
- Add shared Python fixtures, TLS recovery/cancellation tests, a read-trading
  example and caller-owned cache reconciliation guidance.

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
