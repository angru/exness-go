# Exness Go

Unofficial, read-only Go SDK for the public Exness Trading API (contract 1.0.2).
Initial alpha; no tagged release yet and not verified against a live account.
Go **1.26+**, standard library only; no third-party module dependencies.

Supports signed Gateway discovery, account details, instrument names, trading
snapshot, API limits, and paginated orders/deals history. WebSocket, candles,
instrument conditions and trading mutations are follow-up work.

## Installation

In your Go module:

```sh
go get github.com/angru/exness-go@main
```

Go records the resolved pseudo-version in your module. For local development,
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

## Data and error semantics

- Money, prices and volumes use `Decimal`, retaining the exact wire string.
  `String()` returns it; `Rat()` converts to `math/big.Rat` for exact arithmetic.
  Numbers, scientific notation, NaN and null are rejected in required decimal
  fields. Optional decimals use pointers and accept null. An unset `Decimal` is
  not a numeric zero; construct values with `NewDecimal`.
- Time values use `time.Time`. History request times are sent in UTC. Optional
  fields use pointers to distinguish absent values from zero values.
- `encoding/json` checks field types; the client also checks essential required
  data such as account identity, balances and history entities. This is not a full
  runtime JSON Schema validator. Unlike Python's Pydantic models, unknown response
  fields are discarded and unknown response enum strings are retained. Request
  filter enums are validated before network calls.
- All requests take a `context.Context`. Default total HTTP timeout: 15 seconds;
  override with `Config.Timeout`. Responses are limited to 16 MiB.
- `Client` supports concurrent calls and serializes cached discovery. Explicit
  `Discover(ctx, true)` refreshes the host. `Close` closes idle connections and
  prevents new operations; in-flight calls can finish. Custom transports must be
  safe for concurrent use. Environment proxies are ignored by the default transport.
- No SDK retry loop is installed; HTTP errors including 429 are returned directly.
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
Tests use synthetic keys and mocked RoundTrippers; no credentials or network are
required. JSON fixtures are copied from the [Python SDK](https://github.com/angru/exness-python/tree/main/tests/fixtures) and must
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

This is not an official Exness product or a MetaTrader wrapper.

See [ROADMAP.md](ROADMAP.md) for planned work without a live account. SDK code is
available under the [MIT license](LICENSE). The public RFC 8032 test key is
attributed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
