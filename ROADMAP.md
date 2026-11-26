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

## Implemented offline: trading operations

See [TRADING_WRITES.md](TRADING_WRITES.md) for the shared design and acceptance criteria.

- [x] Stage 1: exact-byte mutation signing, explicit keys, opening-position and ACK models.
- [x] Stage 2: five submission methods, remaining request types and unknown-outcome handling.
- [x] Stage 3: bounded result tracking and event correlation.
- [x] Stage 4: offline recovery examples and cross-SDK scenarios (8 scenarios, 49 checkpoints).
- [ ] Stage 5: explicitly authorized live interoperability when an account is available.

Stages 1–4 do not require live access. Registry publication remains deferred.

<a id="next-offline-robustness-and-consumer-verification"></a>

## Completed: offline robustness and consumer verification

All five follow-up work packages are implemented and verified offline in both
SDKs. The linked issues record implementation and check evidence. No live requests,
automatic write retries or releases were part of this work. The table retains
the agreed acceptance criteria; new work should be tracked separately.

| Order | Work package | Acceptance criteria |
| --- | --- | --- |
| 1 | Response validation audit | Review critical REST/transaction/HMR identifiers, required fields, nulls and states against the pinned contract; fix demonstrated inconsistencies; share positive and negative fixtures; document intentional language differences |
| 2 | Decoder fuzzing and generated sequences | Bounded malformed REST/WebSocket inputs and snapshot/event sequences; no panics, unbounded processing or invalid state transitions; save discovered regressions and run deterministic seeds in normal CI |
| 3 | Automated cross-SDK comparison | Detect drift in shared fixture copies and compare normalized results for agreed scenarios; explicitly pin the peer revision for reproducibility; keep each SDK independently buildable |
| 4 | Local lifecycle stress and benchmarks | Repeat reconnect, concurrent request and cancellation scenarios against local servers; verify owned resource cleanup; record reproducible parsing/signing measurements without flaky wall-time performance gates |
| 5 | Installed-package consumer checks | Exercise current exports, request/response types and trading/wait entry points from an installed Python artifact and an independent Go module; update API review tables/counts; run without live credentials |

- [x] 1. [Response validation audit and shared regression corpus](https://github.com/angru/exness-go/issues/1).
- [x] 2. [Decoder fuzzing and generated event sequences](https://github.com/angru/exness-go/issues/2).
- [x] 3. [Automated fixture and behavioral parity checks](https://github.com/angru/exness-go/issues/3).
- [x] 4. [Local lifecycle stress and reproducible benchmarks](https://github.com/angru/exness-go/issues/4).
- [x] 5. [Installed-package checks and current public API documentation](https://github.com/angru/exness-go/issues/5).

Cross-SDK comparison now covers 46 shared files, 170 decoder outcomes and 49
recovery checkpoints, with a pinned-peer CI job in each repository. See
[PARITY.md](PARITY.md) and the [implementation](https://github.com/angru/exness-go/commit/41b8dc0ffe9fef8c7721e3f0f536201bd2d35823).

Six-cycle TLS lifecycle checks and four parsing/signing benchmarks are now
implemented in both SDKs. See [PERFORMANCE.md](PERFORMANCE.md) for resource
accounting, finite test budgets, commands and initial measurements.

Isolated Python-wheel and independent Go-module consumers now exercise the
current submission/wait APIs and typing. See [CONSUMERS.md](CONSUMERS.md) and the
updated [API_REVIEW.md](API_REVIEW.md); historical alpha-7 tags are unchanged.

The first audit fixed uint64 overflow and related critical-field inconsistencies.
See [RESPONSE_VALIDATION.md](RESPONSE_VALIDATION.md) for the implemented rules and
the 170-case shared regression corpus. Do not tighten unknown
fields or optional values merely to make the languages identical; changes require
contract evidence and a regression case. Treat the pinned contract as the offline
source of truth, recording ambiguities explicitly.

## Next: public API compatibility gate

- [ ] [Guard public API compatibility against a pinned baseline](https://github.com/angru/exness-go/issues/6).
- Use current main at `73e62179739ce2e28318f5916ce51b5e5fbeb99f` as the proposed baseline,
  subject to explicit review of the public surface. Historical alpha-7 tags omit
  later submission/wait APIs and are not this baseline.
- Detect incompatible public export, signature and request/response type changes;
  distinguish compatible additions and explain language-specific exclusions.
- Provide an offline command with symbol-level diagnostics, breaking-change
  regressions and a CI gate. Include a break that existing sync/async parity would miss.
- Keep baseline replacement an explicit reviewed edit; retain installed-consumer
  checks and document that API shape checks do not prove behavioral compatibility.

This work requires no live account or release. Implementation routing (direct or
Worker Board) is being selected before dispatch; each SDK owns its own changes.

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
