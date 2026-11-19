# Roadmap

The baseline is verified offline with synthetic fixtures and an independently
signed EXN test vector. Live interoperability remains unverified.

## Completed: read-only REST parity

Instrument conditions, candle history and operation status now complete all nine
read-only REST operations in the pinned contract, plus discovery. Shared fixtures
cover bid/ask/both candles and all operation states. Exact instrument monetary
values and optional fields are preserved; malformed query combinations fail before
network calls. The upstream candle oneOf ambiguity is documented in README.md.

## Next: strengthen offline verification

- Native Go fuzz tests for decimal JSON, key parsing, URLs and pagination metadata.
- A local HTTP server for cancellation, timeout and connection-reuse scenarios.
- Extend malformed-response checks and cross-language compatibility cases.
- Review contract changes explicitly before updating models and fixtures.

## Streaming

- Start with ticks, then trading events.
- Design cancellation, backpressure, bounded reconnects, subscription replay and
  gap reporting before choosing a WebSocket dependency.
- Test fake-server disconnects, slow consumers and malformed messages.

## Release gates

- CI on supported Go versions and successful consumption by a separate module.
- Optional read-only live smoke testing, disabled by default; clearly disclose
  unverified live behavior until it has been exercised.
- Tagged releases follow compatibility review; stable API claims require evidence.
- Trading writes require a separate idempotency and operation-lifecycle design.
  HTTP ACKs must not be treated as execution success.
