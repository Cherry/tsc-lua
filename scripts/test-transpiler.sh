#!/usr/bin/env bash
# Runs each Go program in transpiler/testdata with Go and with the Lua translation, comparing output.
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Native Windows tools need E:/ style paths rather than MSYS /e/ paths.
if pwd -W >/dev/null 2>&1; then
	ROOT="$(cd "$ROOT" && pwd -W)"
fi
LUA="${LUA:-luajit}"
cd "$ROOT/transpiler"
names=("$@")
if [ ${#names[@]} -eq 0 ]; then
	for d in testdata/*/; do
		names+=("$(basename "$d")")
	done
fi
pass=0
fail=0
for name in "${names[@]}"; do
	out="$ROOT/build/test/$name"
	rm -rf "$out"
	if ! go run . -C testdata -o "$out" -natives ../runtime/natives -embed "$ROOT/build/test/embed" "./$name" >"$ROOT/build/test/$name.transpile.log" 2>&1; then
		echo "FAIL $name (transpile)"
		tail -5 "$ROOT/build/test/$name.transpile.log"
		fail=$((fail + 1))
		continue
	fi
	syntax=0
	if ! find "$out" -name '*.lua' | "$LUA" -e "for f in io.lines() do local ok, err = loadfile(f) if not ok then print(err) os.exit(1) end end"; then
		syntax=1
	fi
	expected="$(cd testdata && go run "./$name" 2>&1)"
	actual="$(cd "$ROOT" && "$LUA" runtime/run.lua "$out" "ext.golua_test.$name" 2>&1)"
	if [ $syntax -eq 0 ] && [ "$expected" == "$actual" ]; then
		echo "ok   $name"
		pass=$((pass + 1))
	else
		echo "FAIL $name"
		diff <(echo "$expected") <(echo "$actual") | head -40
		fail=$((fail + 1))
	fi
done
echo "passed $pass, failed $fail"
[ $fail -eq 0 ]
