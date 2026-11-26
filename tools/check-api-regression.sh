#!/bin/sh
# Exercise real production declarations in a disposable, offline module copy.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/exness-api.XXXXXX")
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
export GOWORK=off GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
cd "$root"
go build -o "$scratch/apicheck" ./tools/apicheck
mkdir "$scratch/sdk"
cp "$root"/*.go "$root/go.mod" "$root/go.sum" "$scratch/sdk/"
mkdir -p "$scratch/sdk/testdata/api-baseline"
cp "$root"/testdata/api-baseline/*.go "$scratch/sdk/testdata/api-baseline/"
cd "$scratch/sdk"
"$scratch/apicheck"
printf '\nfunc CompatibleAddition() {}\n' >> client.go
"$scratch/apicheck"
# The method is not invoked by the parity runner; renaming it still compiles.
python3 -c 'from pathlib import Path; p=Path("client.go"); s=p.read_text(); old="func (c *Client) GetLimits("; assert s.count(old) == 1; p.write_text(s.replace(old, "func (c *Client) RenamedLimits("))'
if "$scratch/apicheck" > "$scratch/result" 2>&1; then
    echo 'Broken public method unexpectedly accepted' >&2
    exit 1
fi
if ! grep -F 'Client.GetLimits: removed from method set' "$scratch/result"; then
    cat "$scratch/result" >&2
    exit 1
fi
echo 'Real-source compatibility regressions passed'
