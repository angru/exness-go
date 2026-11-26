#!/bin/sh
# Compile an independent consumer against an isolated copy of production sources.
set -eu
sdk_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
export GOWORK=off
cd "$sdk_root"
go mod download
mkdir "$scratch/sdk" "$scratch/consumer"
for source in "$sdk_root"/*.go; do
    case "$source" in *_test.go) continue ;; esac
    cp "$source" "$scratch/sdk/"
done
cp "$sdk_root/go.mod" "$sdk_root/go.sum" "$sdk_root/LICENSE" "$scratch/sdk/"
cp "$sdk_root/consumers/consumer_test.go" "$sdk_root/consumers/go.mod" "$scratch/consumer/"
cp "$sdk_root/go.sum" "$scratch/consumer/go.sum"
cd "$scratch/consumer"
go mod edit "-replace=github.com/angru/exness-go=$scratch/sdk"
export GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
go test -mod=readonly -race -count=1 -timeout=30s -v .
cp "$sdk_root/consumers/invalid_types.go.txt" invalid_types.go
if go test -mod=readonly -run '^$' . >negative.txt 2>&1; then
    echo 'Invalid consumer unexpectedly compiled' >&2
    exit 1
fi
if ! grep -q 'cannot use.*as exness.OpenPositionRequest' negative.txt; then
    cat negative.txt >&2
    exit 1
fi
echo 'Independent Go consumer: invalid request type rejected'
