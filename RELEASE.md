# Exness Go v0.1.0-alpha.7

Go 1.26+. Module `github.com/angru/exness-go`; WebSocket transport uses
`github.com/coder/websocket v1.8.15` with no transitive module dependencies.

## Install the tagged module

```sh
go get github.com/angru/exness-go@v0.1.0-alpha.7
```

Use the explicit prerelease version rather than `@main` for repeatable builds.
GitHub draft assets include a source archive, API review and SHA256SUMS. Verify
assets with `shasum -a 256 -c SHA256SUMS` from their directory; Go module downloads
also use the standard module checksum mechanism.

## Included functionality

- Signed discovery and all nine read-only REST operations from Trading API 1.0.2.
- Order/deal pagination, precise monetary values, typed responses and errors.
- Five WebSocket streams: ticks, transactions, account state, instruments and HMR.
- Opt-in bounded reconnects and visible gaps; fresh transaction/HMR snapshots after
  reconnect. Dedicated connections, cancellation and credential-safe error strings.

## API review changes

`StreamOptions` and `StreamGap` are the neutral names for shared transport settings
and gap events. All older tick/trading/server aliases remain supported. There are
no removed methods or renamed response fields. Python now rejects new work as soon
as `aclose()` starts, including while socket cleanup is still pending. The review
also records intentional Python/Go differences in validation, timeouts, buffering
and client lifecycle; see [API_REVIEW.md](API_REVIEW.md).

## Compatibility and limits

This is an **unofficial alpha**, not a stable or production-verified SDK. Public
APIs may change in later alphas with release notes. The pinned contract is API 1.0.2
(SHA-256 `ebde19011ffbd30240e918f9369c8233fc9c5a4afb27eca0462b40a141d0f1b3`).
Live Exness interoperability has not been tested. There are no trading writes,
implicit trade retries, durable replay, automatic cache reconciliation or
exactly-once guarantees. Tests use only synthetic credentials and local servers.

Python `0.1.0a7` and Go `v0.1.0-alpha.7` are the first tagged release pair; the shared
alpha number follows six source-only development iterations. Tags identify fixed
commits and must not be moved. GitHub release entries are prepared as drafts marked
prerelease; publishing a draft or uploading to a package registry is a separate
step. Source tags can already be installed explicitly.

## Reproduce validation

```sh
git checkout v0.1.0-alpha.7
gofmt -l .
go vet ./...
go test -race -cover ./...
go build ./...
go mod verify
```

CI covers Go 1.26 and 1.27 and bounded native fuzzing on 1.27. A separate consumer
module verifies tagged installation and all five stream adapters. Shared fixtures
are independently validated against the pinned contract in the Python repository;
Go builds do not depend on that checkout.
