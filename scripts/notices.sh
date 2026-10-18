#!/usr/bin/env bash
# Writes THIRD_PARTY_NOTICES.txt: the licenses of the code compiled into the tsc-lua executable
# (TypeScript, the Go standard library and modules, LuaJIT). Run after changing upstream.lock.
# Needs the upstream checkout (scripts/fetch-upstream.sh) and its modules in the Go cache.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
UPSTREAM="$ROOT/upstream/TypeScript"
MODCACHE="$(go env GOMODCACHE)"
OUT="$ROOT/THIRD_PARTY_NOTICES.txt"

# The upstream module's version of a dependency.
version() {
	awk -v m="$1" '$1 == m { print $2 }' "$UPSTREAM/tsc/go.mod"
}

section() { # title, license file
	printf '\n%s\n%s\n\n' "$1" "$(printf '%*s' "${#1}" '' | tr ' ' '=')"
	tr -d '\r' <"$2"
}

{
	echo "tsc-lua includes code from the following projects, under their licenses."
	section "TypeScript (https://github.com/microsoft/TypeScript)" "$UPSTREAM/LICENSE.txt"
	section "TypeScript notices" "$UPSTREAM/NOTICE.txt"
	section "Go (https://go.dev)" "$(go env GOROOT)/LICENSE"
	for m in github.com/google/go-cmp github.com/peter-evans/patience golang.org/x/sync golang.org/x/text github.com/zeebo/xxh3; do
		section "$m $(version "$m")" "$MODCACHE/$m@$(version "$m")/LICENSE"
	done
	# LuaJIT's notice includes the license of the Lua 5.1/5.2 code it contains.
	section "LuaJIT (https://luajit.org)" "$ROOT/vendor/luajit/COPYRIGHT"
} >"$OUT"
echo "wrote $OUT"
