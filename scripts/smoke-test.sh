#!/usr/bin/env bash
# Checks that the executable from scripts/build-binary.sh starts and reports a type error.
set -euo pipefail
exe=build/bin/tsc-lua
[ -f "$exe.exe" ] && exe="$exe.exe"
"$exe" --version
mkdir -p build/smoke-test
printf 'const x: number = "nope";\n' > build/smoke-test/a.ts
set +e
"$exe" --ignoreConfig --noEmit build/smoke-test/a.ts
status=$?
set -e
[ "$status" -eq 2 ] || {
	echo "expected exit code 2, got $status"
	exit 1
}
