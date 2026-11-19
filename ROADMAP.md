# Roadmap

The baseline is verified offline with synthetic fixtures and an independently
signed EXN test vector. Live interoperability remains unverified.

## Next: read-only REST parity

- Instrument conditions, candle history and operation status, following the
  documented API operation IDs and the Python SDK's shared contract fixtures.
- Preserve exact decimal values and typed optional fields. Validate request
  constraints before network calls.
- Keep response validation behavior and any Python/Go differences documented.

Acceptance: shared fixtures, signing/URL checks, gofmt, vet, race-enabled tests and
standalone module/example builds pass. No live account is needed for this stage.

## Strengthen offline verification

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
