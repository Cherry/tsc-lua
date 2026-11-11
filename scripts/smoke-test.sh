#!/usr/bin/env bash
# Checks that the executable from scripts/build-binary.sh starts and reports a type error, and
# that checking in copies of the process reports the same as one process.
set -euo pipefail
exe=build/bin/tsc-lua
[ -f "$exe.exe" ] && exe="$exe.exe"
"$exe" --version
mkdir -p build/smoke-test
printf 'const x: number = "nope";\n' > build/smoke-test/a.ts
printf 'export const y: string = 1;\n' > build/smoke-test/b.ts
check() {
	set +e
	"$exe" --ignoreConfig --noEmit build/smoke-test/a.ts build/smoke-test/b.ts
	status=$?
	set -e
	[ "$status" -eq 2 ] || {
		echo "expected exit code 2, got $status"
		exit 1
	}
}
check
TSCLUA_CHECK_PROCESSES=1 check > build/smoke-test/one-process.txt
TSCLUA_CHECK_DEBUG=1 check > build/smoke-test/copies.txt 2> build/smoke-test/copies.log
cat build/smoke-test/copies.log
cmp build/smoke-test/one-process.txt build/smoke-test/copies.txt
# A machine with one processor or little memory makes no copies, which is not a failure.
if grep -q 'no usable result\|could not be made' build/smoke-test/copies.log; then
	echo "a copy of the process failed"
	exit 1
fi
