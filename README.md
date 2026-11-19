# Exness Go

Unofficial, read-only Go SDK for the public Exness Trading API (contract 1.0.2).
Alpha v0.1.0-alpha.7; not verified against a live account.
Go **1.26+**. WebSocket transport uses [coder/websocket](https://github.com/coder/websocket)
(v1.8.15); REST and signing use the standard library.

Supports signed Gateway discovery, account details, instrument names and conditions,
trading snapshot, API limits, paginated orders/deals history, candle history and
existing operation status. All nine read-only REST operations in the pinned
contract are covered, plus discovery, live WebSocket ticks, transactions and account/instrument/HMR
events. Trading mutations remain future work.

## Installation

In your Go module:

```sh
go get github.com/angru/exness-go@v0.1.0-alpha.7
```

Go records the explicit prerelease version in your module. For local development,
clone the repository and optionally use a `replace` directive pointing to your checkout.

```go
import (
    "context"
    "os"
    exness "github.com/angru/exness-go"
)

credentials, err := exness.NewCredentials(
    os.Getenv("EXN_API_KEY"), os.Getenv("EXN_PRIVATE_KEY"),
)
if err != nil { return err }
client, err := exness.NewClient(exness.Config{
    Credentials: credentials,
    AccountID: os.Getenv("EXN_ACCOUNT_ID"),
    DiscoveryURL: os.Getenv("EXN_DISCOVERY_BASE_URL"),
})
if err != nil { return err }
defer client.Close()
account, err := client.GetAccount(context.Background())
```

This fragment belongs inside a function returning `error`. A complete runnable
example is `go run ./examples/read-account`. Configure the same four `EXN_*`
variables as the Python SDK. `EXN_PRIVATE_KEY` is the Exness 128-character hex key
or full unencrypted Ed25519 PKCS#8 PEM. The discovery URL must come from the DNS
widget in Key Management. Only HTTPS origins are accepted; signed redirects are
never followed. API keys need Trading read access.

## History

```go
start := time.Now().UTC().Add(-time.Hour)
query := exness.DealHistoryQuery{
    HistoryQuery: exness.HistoryQuery{From: &start, Limit: 100},
    DealType: exness.DealBalance,
}
for item, err := range client.IterDealsHistory(ctx, query) {
    if err != nil { return err }
    fmt.Println(item.Deal.DealID)
}
```

Use `GetOrdersHistory` / `GetDealsHistory` for individual pages. Cursor requests
accept only `Cursor` and `Limit`. Iterators drop initial filters automatically,
preserve opaque tokens and stop on repeated/inconsistent cursors. Breaking the
loop prevents the next page request. Earlier rows may have been yielded before an
error. Choose a time window within the current `GetLimits` history constraints.

## Candles, conditions and operation status

```go
conditions, err := client.GetInstrumentConditions(ctx, "EURUSD")
if err != nil { return err }
fmt.Println(conditions.PointDigits, conditions.VolumeStep)

count := 10
candles, err := client.GetCandleHistory(ctx, exness.CandleQuery{
    Instrument: "EURUSD", Timeframe: exness.TimeframeM1, From: start,
    Count: &count, PriceType: exness.PriceBoth,
})
if err != nil { return err }
for _, candle := range candles.Candles {
    fmt.Println(candle.Time, candle.Bid, candle.Ask)
}

// Read an operation ID you already have. This does not submit a trade.
operation, err := client.GetOperationStatus(ctx, "789")
if err != nil { return err }
fmt.Println(operation.Status, operation.IsTerminal())
```

Candles require `From` and exactly one of `To` (exclusive) or a nonzero `Count`.
Negative counts are valid. Empty `PriceType` defaults to bid; ask and both are also
supported. Timeframes: S1, S5, S15, M1, M3, M5, M10, M15, M30, H1, H2, H4, D1, W1, MN.
Current count/range caps come from `GetLimits` and are enforced by the server;
the method makes no hidden limits request and does not split, sort or retry.

Candle OHLC and volume are JSON doubles, exposed as `*float64` to distinguish zero
from missing data. Bid/ask-only responses use flat OHLC; both-side responses use
nested `Bid` and `Ask`. The upstream oneOf is ambiguous because both branches have
only optional fields and allow extra fields. The SDK preserves both shapes; Python
contract tests document the defect and validate a request-selected branch without
modifying the vendored YAML. See the [contract notes](https://github.com/angru/exness-python/blob/main/contracts/README.md#candle-schema-ambiguity).

Conditions retain exact `Decimal` volume, swap and commission fields. Optional
settings remain nil. Operation status is read once without polling; pending,
confirmed, rejected and failed are recognized. `IsTerminal` means processing ended,
not success. Rejection/failure codes remain model fields; HTTP failures return
`APIError`. Run `go run ./examples/read-market` for a complete example with optional
`EXN_INSTRUMENT` and `EXN_OPERATION_ID` environment inputs.

## Data and error semantics

- String-encoded money, prices and volumes use `Decimal`, retaining the exact wire string.
  `String()` returns it; `Rat()` converts to `math/big.Rat` for exact arithmetic.
  Numbers, scientific notation, NaN and null are rejected in required decimal
  fields. Optional decimals use pointers and accept null. An unset `Decimal` is
  not a numeric zero; construct values with `NewDecimal`.
- Time values use `time.Time`. History request times are sent in UTC. Optional
  fields use pointers to distinguish absent values from zero values.
- `encoding/json` checks field types; the client also checks essential required
  data such as account identity, balances and history entities. This is not a full
  runtime JSON Schema validator. Unlike Python's Pydantic models, unknown response
  fields are discarded and unknown instrument/trading enum strings are retained.
  Operation states are checked against pending/confirmed/rejected/failed. Request
  filter enums are validated before network calls. Dot-only instrument path names
  are rejected to prevent URL normalization; operation IDs use uint64 syntax and
  numeric identity comparison, allowing leading zeros.
- All requests take a `context.Context`. Default total REST timeout: 15 seconds;
  override with `Config.Timeout`. REST responses are limited to 16 MiB. Tick streams
  have a separate handshake timeout and message limit, described below.
- `Client` supports concurrent calls and serializes cached discovery. Explicit
  `Discover(ctx, true)` refreshes the host. Callers waiting for discovery honor
  their own context; cached reads can continue while an explicit refresh runs.
  `Close` does not wait for discovery, closes idle connections and
  prevents new operations. In-flight REST calls can finish; active WebSocket streams
  are canceled, including their discovery and reconnect waits. Custom transports must be
  safe for concurrent use. Environment proxies are ignored by the default transport.
- REST calls have no SDK retry loop; HTTP errors including 429 are returned directly.
  The Go HTTP transport may recover stale pooled connections for safe GETs.
- Use `errors.As` for `*APIError`, `*ProtocolError`, `*TransportError`.
  `APIError` exposes status, optional numeric code/message and raw Retry-After.
  Error strings do not echo credentials or raw response bodies. `ErrorMessage`
  and an unwrapped transport cause may contain untrusted details; do not log them
  indiscriminately. `errors.Is(err, context.Canceled)` works through transport errors.

## Development

```sh
gofmt -l .
go vet ./...
go test -race -cover ./...
go build ./...
```

CI checks formatting, vet, race-enabled tests and build on Go 1.26 and 1.27.
Native fuzz targets cover exact decimal arithmetic/JSON, key parsing, HTTPS origins,
cursor encoding/signing and pagination termination. Seeds run with ordinary tests;
CI also fuzzes each target for five seconds on Go 1.27. To explore longer locally:

```sh
go test -run '^$' -fuzz '^FuzzDecimal$' -fuzztime 1m -parallel 2 .
```

Tests use synthetic keys, mocked RoundTrippers and loopback HTTPS servers with
test certificates. No Exness account or external service is required, but local
socket binding must be allowed. Real connections exercise cancellation (including
queued discovery), header/body deadlines, truncated responses, redirect refusal,
signature verification, connection reuse and idle-connection closure.
JSON fixtures are copied from the [Python SDK](https://github.com/angru/exness-python/tree/main/tests/fixtures) and must
be kept in sync. `signing-vector.json` was independently signed by OpenSSL and is
checked byte-for-byte by both SDKs. Python tests validate response fixtures against
the pinned OpenAPI snapshot; Go tests consume the same fixtures independently.

## Contract sources

Retrieved 2026-11-19 (Asia/Nicosia):

- [Trading OpenAPI 1.0.2](https://www.exness-api.com/openapi/trading.public.yaml)
  SHA-256: `ebde19011ffbd30240e918f9369c8233fc9c5a4afb27eca0462b40a141d0f1b3`.
  The full snapshot is in the [Python repository's contracts directory](https://github.com/angru/exness-python/tree/main/contracts);
  this module does not depend on that directory at build or runtime.
- [Discovery](https://www.exness-api.com/trading/get-started/access-point-host-discovery).
- [Key loading and signing examples](https://www.exness-api.com/trading/guides/code-examples).

For contract maintenance, use the shared checker from a separate Python SDK checkout:

```sh
cd /path/to/exness-python
uv sync --locked
uv run python -m tools.check_contract
# Offline candidate review:
uv run python -m tools.check_contract --candidate /path/to/candidate.yaml
```

It reports operations, parameters and model changes without updating the snapshot.
Exit codes: 0 unchanged, 1 review required, 2 check failed. The Python repository
also provides a manually triggered Contract review workflow with downloadable
JSON reports and candidate YAML. See the [maintenance instructions](https://github.com/angru/exness-python/blob/main/contracts/README.md#checking-for-upstream-changes).
This is development tooling, not a Go dependency. Review shared-schema changes
in both SDKs, keep synthetic fixtures aligned, and update the provenance hash here
only after explicitly adopting a reviewed snapshot. Structural differences are
not automatically classified as breaking or compatible.

This is not an official Exness product or a MetaTrader wrapper.

## Live ticks

```go
for event, err := range client.StreamTicks(ctx, []string{"EURUSD"}, exness.TickStreamOptions{
    MaxReconnects: 3, // Opt in; the default is no reconnects.
}) {
    if err != nil { return err }
    if event.Gap != nil {
        fmt.Printf("possible tick loss; reconnect %d in %s\n",
            event.Gap.ReconnectAttempt, event.Gap.Delay)
        continue
    }
    if event.Tick.Bid != nil {
        fmt.Println(*event.Tick.Instrument, *event.Tick.Bid)
    }
}
```

Run `go run ./examples/read-ticks` with the same four `EXN_*` variables and optional
`EXN_INSTRUMENT` (default `EURUSD`). Ctrl-C cancels the stream. The SDK discovers the
account host, signs the exact WebSocket GET path and subscribes with a fresh request
ID on every connection. Instrument lists are copied when `StreamTicks` is called;
validation and networking are lazy, beginning when the iterator is consumed.

`Tick` follows the pinned `WSTick` schema: instrument is required by the SDK and
must belong to the subscription; bid, ask and timestamp are optional. Prices are
JSON doubles (`*float64`), as with candle OHLC, rather than string decimals. Tick,
subscription and error fixtures are shared with Python. Unknown fields are ignored.
A terminal failure yields one error and ends iteration; earlier ticks remain valid.

- Reconnects are disabled by default. `MaxReconnects` accepts 0–1000 and applies
  across the whole iteration, including successful connections. It never resets
  when a tick arrives. Backoff starts at one second and doubles up to 30 seconds;
  customize `ReconnectDelay` and `MaxReconnectDelay` to positive durations.
- A `Gap` is yielded **before** every retry delay. It contains the attempt, delay
  and optional close code (nil when no connection was established). It signals
  possible loss, not a count of missed ticks. There is no replay, sorting or
  deduplication. Reconnects resubscribe using cached discovery; no hidden host refresh.
- Network failures and close codes 1001, 1006, 1011 and 1012 may reconnect within
  the budget. A normal 1000 close ends successfully. Other close codes, protocol
  errors, subscription errors, TLS validation errors and all HTTP handshake
  failures (including 429 and 503) are terminal. Discovery failures are not retried.
- `OpenTimeout` defaults to 15 seconds and applies only to each handshake.
  The caller context bounds the stream lifetime. There is no tick inactivity timeout.
  Pings run every 20 seconds with a 20-second pong deadline.
- Reading is driven by iteration, with transport backpressure and no SDK tick queue.
  Keep the loop responsive: pong processing also needs reads, so a stalled consumer
  may lose its connection. This differs from Python's bounded background frame queue.
- `MaxMessageBytes` defaults to 64 KiB and accepts 1 byte through 16 MiB, applying
  to incoming messages and the outgoing subscription. Oversized incoming messages
  end with `StreamClosed{Code: 1009}`. An oversized subscription fails before dialing.
- Breaking iteration, canceling the context or calling `Client.Close` releases the
  socket promptly. Local cleanup closes the socket without waiting for a peer close
  handshake. Cancellation is inspectable with `errors.Is(err, context.Canceled)`.
  Closing the client also releases sockets while consumer code is paused; it cannot
  interrupt that consumer's own code.
- Use `errors.As` for `*StreamClosed`, `*WebSocketAPIError`, `*APIError`,
  `*ProtocolError` and `*TransportError`. Close reasons and subscription error text
  are excluded from error strings. Do not indiscriminately log underlying causes
  or server-provided error fields.

Redirects and compression are disabled; signed headers are never forwarded across
redirects. The default transport ignores environment proxies. `Config.Transport`
is shared by REST and WebSocket requests; custom transports must support an HTTP/1.1
upgrade with a writable response body (as `http.Transport` does). TLS settings come
from that transport. Live Exness interoperability remains unverified; tests use local
TLS servers for signatures, subscriptions, reconnection, cancellation, ping/pong,
slow consumers, message limits, certificate validation and handshake deadlines.

See [ROADMAP.md](ROADMAP.md) for planned work without a live account. SDK code is
available under the [MIT license](LICENSE). The public RFC 8032 test key is
attributed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Trading events

```go
for event, err := range client.StreamTrading(ctx, exness.TradingStreamOptions{MaxReconnects: 3}) {
    if err != nil { return err }
    switch {
    case event.Gap != nil:
        fmt.Println("State is stale; waiting for a fresh snapshot")
    case event.Snapshot != nil:
        fmt.Println("New baseline", len(event.Snapshot.Positions))
    case event.Transaction != nil:
        fmt.Println("Changed positions", len(event.Transaction.Payload.Positions))
    }
}
```

Run `go run ./examples/read-trading` with the same four `EXN_*` variables.
`TradingStreamEvent` contains exactly one `Snapshot`, `Transaction` or `Gap` when
there is no error. `TradingStreamOptions` and `TradingStreamGap` alias their tick
counterparts. Breaking iteration or canceling the context releases the connection.

The `transactions` subscription sends a full `trading_state_snapshot`, followed by
`transaction_event` deltas. The SDK requires a valid snapshot before yielding any
transaction on **every** connection, including after reconnect. A transaction
received before that boundary is a terminal protocol error. Repeated snapshots
are accepted as replacement baselines. Each subscription uses a fresh request ID.

Recovery belongs to the caller's application state:

1. On a gap, mark the previous cache stale immediately; on termination (including
   normal close), it is no longer live either.
2. Replace cached active orders, open positions and account state with the next
   snapshot. Do not merge a new baseline into stale state.
3. Apply subsequent transaction payloads as **changed entities**, not full lists.
   Retain an open position after a partial close; remove closed positions and
   terminal orders from an active-state view. Missing entities are not deletions.
4. Use order/deal history separately if missed historical changes matter. Snapshots
   cannot reconstruct missed transactions. There is no resume token, replay or
   exactly-once guarantee; the SDK does not merge, deduplicate or poll implicitly.

`status` is optional: `success`, `rejected` and `failed` are supported. Rejected or
failed operations with empty payloads are still **data events**, even when they
contain `error_message`; subscription errors instead raise/return `WebSocketAPIError`.
An absent status is not inferred as success. `operation_id` can be absent for
balance or system changes. Monetary values remain exact decimals; transaction
account state also requires `leverage`. Server error fields are untrusted data.

Transport behavior is shared with ticks: reconnects are opt-in and lifetime-bounded,
gaps precede retries, HTTP/TLS/protocol failures are terminal, and cancellation and
client closure release sockets. The default message cap is 64 KiB, including the
initial snapshot; explicitly increase it up to 16 MiB for larger accounts. There
is no snapshot-arrival timeout beyond the caller's stream deadline and heartbeat.
Other server subscriptions are documented below.
Live Exness interoperability has not been verified.

## Account, instrument and HMR events

```go
for event, err := range client.StreamHMR(ctx, []string{"EURUSD"}, exness.ServerStreamOptions{
    MaxReconnects: 3,
}) {
    if err != nil { return err }
    switch {
    case event.Gap != nil:
        fmt.Println("HMR state is stale")
    case event.Snapshot != nil:
        fmt.Println("Replace baseline", len(event.Snapshot.Periods))
    case event.Update != nil:
        fmt.Println("Changes", event.Update.UpsertedPeriods, event.Update.RemovedPeriodIDs)
    }
}
```

`StreamAccountState(ctx, options)` yields `AccountStateStreamEvent` with either
`AccountState` or `Gap`. `StreamInstruments(ctx, names, options)` yields
`InstrumentStreamEvent` with either `Instrument` or `Gap`. `StreamHMR` yields
`HmrStreamEvent` with exactly one `Snapshot`, `Update` or `Gap` on success.
`ServerStreamOptions` and `ServerStreamGap` alias the tick equivalents. Break or
cancel the context to close a stream. Run an example with the four `EXN_*`
variables already configured:

```sh
EXN_STREAM=account_state go run ./examples/read-events
EXN_STREAM=instruments EXN_INSTRUMENTS=all go run ./examples/read-events
EXN_STREAM=hmr EXN_INSTRUMENTS=EURUSD,XAUUSD go run ./examples/read-events
```

Each call opens a dedicated `/ws/events` connection with one subscription and a
fresh request ID. Multiple concurrent calls consume multiple server connections;
check `GetLimits` / `get_limits` when choosing concurrency. These methods do not
multiplex subscriptions or change instrument lists mid-connection. Close the
iterator to unsubscribe by closing its connection; open a new stream to change
selection. Reconnects reuse the copied selection and the existing signed transport.
Options and gap types alias their tick equivalents; retries remain disabled by
default, bounded when enabled, and preceded by visible gaps. Message/queue limits,
heartbeat, redirect refusal and cancellation rules are unchanged.

| Subscription | Events | Initial state and recovery |
| --- | --- | --- |
| `account_state` | `AccountStateEvent` | No initial snapshot. Each event is a fresh floating-state observation; a gap makes the prior observation stale until the next event. |
| `instruments` | `InstrumentEvent`, action `new`, `upd`, `del` | No initial snapshot. `new`/`upd` contain full conditions; `del` removes the instrument from a cache. Missed changes or deletions require explicit reconciliation. |
| `hmr` | `HmrSnapshot`, `HmrUpdate` | A fresh snapshot is mandatory before updates on every connection. Every snapshot, even empty, replaces the entire baseline. |

Instrument subscriptions require a nonempty explicit list or exactly `["all"]`.
HMR requires explicit instruments and rejects `all`. Duplicate or malformed names
fail before networking; account availability is checked by the server, without a
hidden REST lookup. Unsubscribed instrument events and unknown/wrong event kinds
are terminal protocol errors. Lists are copied when constructing the iterator.

An account-state event contains exact balance, equity and used-margin values. It
is an observation, not a transaction history or a substitute for `transactions`.
The SDK does not assume a fixed delivery interval or generate synthetic values.
For instrument caches, treat a gap or termination as loss of continuity. Explicitly
refresh the available instrument list and conditions through REST when needed;
that REST fetch and concurrent WebSocket delivery are **not an atomic snapshot**.
The SDK does not silently merge a REST result with events or mark the entire cache
current merely because one instrument received an update.

HMR arrays live at the event top level, without `payload` or `event_time`.
Snapshots contain `periods`; updates contain `upserted_periods` and
`removed_period_ids`, both required even when empty. Applications maintain their
own map keyed by `period_id`: replace it on a snapshot, upsert changed periods and
remove the listed IDs on updates. Removing an unknown ID is harmless. A gap or
termination invalidates the baseline; the SDK refuses an update before the new
snapshot. It yields data without automatic merging, sorting, replay or deduplication.

A period preserves the server's complete `instruments` list (not filtered locally)
and string leverage. `regular` schedule strings use calendar dates, while `periodic`
strings use weekly schedules such as `Mon, 13:00:00`. Both are preserved verbatim;
no timezone, recurrence calculation or "active now" flag is inferred. All synthetic
fixtures are shared across SDKs and checked against the pinned OpenAPI. Live Exness
interoperability remains unverified.

## Reviewed alpha API

Prefer `StreamOptions` and `StreamGap` for all five stream types. Existing
`TickStream*`, `TradingStream*` and `ServerStream*` aliases remain supported.
[API_REVIEW.md](API_REVIEW.md) maps the full Python/Go surface and records model,
error, lifecycle and timeout differences. [RELEASE.md](RELEASE.md) contains the
alpha release notes, installation instructions and publication procedure.
