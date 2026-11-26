# Public API compatibility gate

The reviewed scope is the root `github.com/angru/exness-go` package at
`73e62179739ce2e28318f5916ce51b5e5fbeb99f` (current main identified in
ROADMAP.md), including submission and wait APIs. Historical alpha tags are not
the baseline. `testdata/api-baseline/` contains the production Go sources from
that commit; it is not generated in CI and has no dependency on the Python SDK.

After preparing the Go toolchain and module cache, run from the repository root:

```sh
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go run ./tools/apicheck
GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go test ./tools/apicheck
sh tools/check-api-regression.sh
```

The checker type-checks both versions using Go's type system and reports sorted
symbol-level diagnostics. It examines exported package declarations, constant
values, named underlying types, interface definitions, exported struct fields
(including tags and embedding), and both value and pointer method sets, including
promoted methods. Parameters and results are compared without parameter names.
New declarations and methods are allowed. Adding a method to an existing interface
is rejected because external implementations may no longer satisfy it.

For a struct with only exported fields, external users can use unkeyed literals:
adding, removing, or reordering fields therefore breaks compatibility even when
keyed literals would survive. For a struct already containing an unexported field,
external unkeyed literals cannot be constructed; adding an exported field is
allowed if existing exported fields and comparability remain compatible. Tags
are checked because request/response serialization can depend on them. Changes
to private fields are otherwise outside the gate. Exact interface and underlying
type comparisons are deliberately conservative and can reject source-compatible
changes; review diagnostics rather than silently accepting them.

The comparison covers the root package selected by the current Go build context;
it does not enumerate other platforms or build tags, cgo, future generic type
parameter constraint changes, runtime semantics, wire behavior, reflection on
private fields, or dependency API changes. It is not a substitute for consumer,
contract, behavior or live interoperability tests. Dependency export data must
already be cached for offline operation. The regression script copies production
sources into a temporary independent module and checks both a compatible addition
and a renamed public method; the latter can leave the existing parity tests intact.

Changing the baseline is a deliberate reviewed compatibility-policy decision,
not a routine response to a failing gate. First review the old-to-new API and
consumer impact, document accepted breaks and their migration, and obtain project
review. Then replace the snapshot with the chosen commit's production root Go
files, update the pinned revision in the checker and this document, and review the
snapshot diff and run the gate and regressions. Do not regenerate it automatically
in CI or replace it merely to make CI pass.
