# Roadmap

The baseline is verified offline with synthetic fixtures and an independently
signed EXN test vector. Live interoperability remains unverified.

## Completed: read-only REST parity

Instrument conditions, candle history and operation status now complete all nine
read-only REST operations in the pinned contract, plus discovery. Shared fixtures
cover bid/ask/both candles and all operation states. Exact instrument monetary
values and optional fields are preserved; malformed query combinations fail before
network calls. The upstream candle oneOf ambiguity is documented in README.md.

## Completed: generated inputs and local HTTPS verification

- Five native fuzz targets cover decimal arithmetic/JSON, key parsing, origins,
  cursor signing and pagination. Seeds run in every test suite; CI fuzzing is bounded.
- Local TLS servers exercise cancellation, deadlines, truncated bodies, redirects,
  connection reuse and closure. Discovery waiters now respect their own context.
- Continue reviewing contract changes and cross-language compatibility explicitly
  before updating models and fixtures.

## Completed: contract maintenance

Contract maintenance now uses the Python repository's explicit checker and manual
review workflow; it reports drift for both SDKs without changing their snapshots
or adding Go runtime/build dependencies. See README.md for invocation details.

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
