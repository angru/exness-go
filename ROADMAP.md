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

## Completed: live tick transport

- Signed WebSocket connections use coder/websocket, the approved transport dependency.
- Pull-based tick iteration supports context cancellation and client closure, opt-in
  bounded reconnects, explicit gap events and fresh subscriptions on reconnect.
- Synthetic TLS tests cover disconnects, slow consumers, malformed messages,
  heartbeat, redirects, untrusted certificates, size limits and handshake deadlines.

## Completed: transactions stream

- Typed transaction deltas and snapshots share the existing signed WebSocket transport.
- Every connection requires a new snapshot before transactions. Gap events invalidate
  caller-owned caches; fresh snapshots replace them. There is no implicit replay.
- Shared Python fixtures and local TLS tests cover partial closes, balance changes,
  rejected results, reconnection boundaries and cancellation.

## Completed: account, instrument and HMR events

- Dedicated typed subscriptions share transport limits, signed handshakes, opt-in
  reconnects and explicit gaps. Instrument lists are copied and validated locally.
- Instrument selection supports exactly ["all"]; HMR uses explicit names only.
- HMR requires a fresh snapshot on every connection and supports empty/replacement
  snapshots and deltas. Schedule strings remain unchanged without inferred timezone.
- Account and instrument streams have no initial snapshot; recovery limitations
  and caller-owned cache reconciliation are explicit in README.md.
- Shared fixtures, pinned-schema checks and local TLS tests cover all event kinds,
  validation, lifecycle actions, reconnection boundaries and cleanup.

## Completed: alpha API review

- Reviewed the REST/stream surface, model locations, error contracts and lifecycle.
- Added neutral stream aliases without removing legacy names; fixed Python close
  admission and added signature/external-consumer regression checks.
- Prepared alpha 7 tags, release notes and reproducible install instructions.
  See API_REVIEW.md and RELEASE.md.

## In progress: trading operations

See [TRADING_WRITES.md](TRADING_WRITES.md) for the shared design and acceptance criteria.

- [x] Stage 1: exact-byte mutation signing, explicit keys, opening-position and ACK models.
- [x] Stage 2: five submission methods, remaining request types and unknown-outcome handling.
- [ ] Stage 3: bounded result tracking and event correlation.
- [ ] Stage 4: offline recovery examples and cross-SDK scenarios.
- [ ] Stage 5: explicitly authorized live interoperability when an account is available.

Stages 1–4 do not require live access. Registry publication remains deferred.

## Deferred: live interoperability and user feedback

- Add an explicitly enabled read-only live smoke workflow when credentials and a
  test account are available; retain the unverified-live disclosure until then.

## Release gates

- CI on supported Go versions and successful consumption by a separate module.
- Optional read-only live smoke testing, disabled by default; clearly disclose
  unverified live behavior until it has been exercised.
- Tagged releases follow compatibility review; stable API claims require evidence.
- Trading writes require a separate idempotency and operation-lifecycle design.
  HTTP ACKs must not be treated as execution success.
