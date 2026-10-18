#!/usr/bin/env bash
# Transpiles the TypeScript 7 compiler (upstream/TypeScript/tsc) plus the tsc-lua entry points to Lua.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPSTREAM="$ROOT/upstream/TypeScript/tsc"
OUT="${OUT:-$ROOT/build/lua}"
EMBED="${EMBED:-$ROOT/build/embed}"

# Go only allows importing internal/ packages from inside the module, so the entry
# points are copied into the (untracked) upstream checkout.
for entry in tsclua tscluatest; do
	mkdir -p "$UPSTREAM/cmd/$entry"
	cp "$ROOT/entry/$entry/"*.go "$UPSTREAM/cmd/$entry/"
done

rm -rf "$OUT"
cd "$ROOT/transpiler"
go run . -C "$UPSTREAM" -o "$OUT" -natives "$ROOT/runtime/natives" -embed "$EMBED" -tags math_big_pure_go,purego "$@" ./cmd/tsclua ./cmd/tscluatest
