#!/usr/bin/env bash
# Builds the standalone tsc-lua executable: LuaJIT + tscnative + an embedded archive of the
# runtime, the transpiled compiler and the TypeScript lib files.
# Requires a prior scripts/transpile.sh. CC defaults to gcc (use clang on macOS).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Native Windows tools need E:/ style paths rather than MSYS /e/ paths.
if pwd -W >/dev/null 2>&1; then
	ROOT="$(cd "$ROOT" && pwd -W)"
fi
CC="${CC:-gcc}"
MAKE="${MAKE:-$(command -v make || command -v gmake || command -v mingw32-make)}"
BUILD="$ROOT/build"
OUTDIR="${OUTDIR:-$BUILD/bin}"
LJ="$BUILD/luajit-static"
SRC="$LJ/src"
EXE=""
LIBS="-lm"
case "$(uname -s)" in
	MINGW* | MSYS* | CYGWIN*)
		EXE=".exe"
		LIBS="-lshell32"
		;;
	Linux*)
		LIBS="-lm -ldl"
		;;
	Darwin*)
		export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"
		;;
esac
mkdir -p "$OUTDIR"
# Optimization flags for the host (LuaJIT itself builds with its own flags).
OPT="${OPT:--O2}"

# A static LuaJIT library; its interpreter packs the archive, so the bytecode matches.
rm -rf "$LJ"
cp -r "$ROOT/vendor/luajit" "$LJ"
# Lua 5.2 compatibility adds table.pack/unpack, package.searchers and __pairs, which the runtime
# uses; tscnative.c includes LuaJIT's internal headers, so it builds with the same flags.
XCFLAGS="-DLUAJIT_ENABLE_LUA52COMPAT"
"$MAKE" -C "$SRC" -j8 CC="$CC" BUILDMODE=static XCFLAGS="$XCFLAGS"

manifest="$BUILD/archive.manifest"
: > "$manifest"
add_modules() {
	local dir="$1"
	while IFS= read -r f; do
		local rel="${f#"$dir"/}"
		local mod="${rel%.lua}"
		mod="${mod//\//.}"
		printf 'm\t%s\t%s\n' "$mod" "$f" >> "$manifest"
	done < <(find "$dir" -name '*.lua' | sort)
}
add_modules "${LUADIR:-$BUILD/lua}"
add_modules "$ROOT/runtime/natives"
printf 'm\trt\t%s\n' "$ROOT/runtime/rt.lua" >> "$manifest"
printf 'm\ttsclua_main\t%s\n' "$ROOT/runtime/tsclua_main.lua" >> "$manifest"
while IFS= read -r f; do
	rel="${f#"$BUILD/embed"/}"
	printf 'd\tembed/%s\t%s\n' "$rel" "$f" >> "$manifest"
done < <(find "$BUILD/embed" -type f | sort)

"$SRC/luajit$EXE" "$ROOT/scripts/pack.lua" "$manifest" "$BUILD/archive.bin"

"$CC" $OPT -std=gnu11 $XCFLAGS -I"$SRC" -DTSCLUA_ARCHIVE_PATH="\"$BUILD/archive.bin\"" \
	-o "$OUTDIR/tsc-lua$EXE" "$ROOT/native/tscnative.c" "$ROOT/native/host.c" "$SRC/libluajit.a" $LIBS
echo "built $OUTDIR/tsc-lua$EXE"
