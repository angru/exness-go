# Exness Go SDK

Unofficial read-only Exness Trading API client. English code and documentation,
Russian user replies. This project owns its Git history. Go 1.26+.

- `testdata/` contains copies of the Python SDK's synthetic contract/signing
  fixtures. Keep behavior aligned; don't import the Python repo at runtime.
- Use official public API contracts; provenance is in README.md.
- Prefer the standard library. `github.com/coder/websocket` is approved for the
  WebSocket transport; keep other dependencies minimal. Keep monetary values exact.
- WebSocket reconnects must be opt-in, bounded and visible as gap events. Never forward
  signed headers across redirects or log them. Context cancellation must propagate.
- Transactions require a fresh snapshot on every connection; processing rejections
  are data events, and gaps invalidate caller-owned caches.
- Check `gofmt -l .`, `go vet ./...`, `go test -race -cover ./...`, `go build ./...`.
- Network tests bind loopback TLS sockets; no live account is used. Keep fuzz
  targets bounded and deterministic; run changed targets with a finite `-fuzztime`.
- Never log private keys or signed headers. No live tests, mutations, automatic
  retries or guessed hostnames as incidental changes.
