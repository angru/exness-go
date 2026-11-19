# Third-party reference material

The synthetic Ed25519 seed and public key used in tests are the published RFC 8032,
section 7.1, TEST 1 values: https://www.rfc-editor.org/rfc/rfc8032#section-7.1.
The EXN protocol signing fixture was independently computed with OpenSSL using
those public test values. It is not a live account credential.

The Exness API contract is upstream reference material, not SDK-authored material.
Its source and snapshot hash are recorded in README.md. This repository does not
claim ownership of or relicense that upstream specification.

WebSocket transport depends on github.com/coder/websocket v1.8.15, distributed
under the ISC license. Its source is fetched as a Go module, not vendored here.
See https://github.com/coder/websocket/blob/v1.8.15/LICENSE.txt for the upstream
copyright and permission notice.
