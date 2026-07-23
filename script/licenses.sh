#!/usr/bin/env bash
# Run go-licenses (pinned via the tool directive in go.mod) against the
# darwin/arm64 release target's dependency graph. The tool must be built for
# the host first: `go tool`/`go run` with GOOS/GOARCH set would cross-compile
# the tool itself and fail to execute on non-darwin hosts.
set -euo pipefail

bindir="$(mktemp -d)"
trap 'rm -rf "$bindir"' EXIT
go build -o "$bindir/go-licenses" github.com/google/go-licenses/v2
GOOS=darwin GOARCH=arm64 "$bindir/go-licenses" "$@"
