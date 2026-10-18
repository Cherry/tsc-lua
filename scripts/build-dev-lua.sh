#!/usr/bin/env bash
# Builds LuaJIT and the tscnative module from vendor/ for development and CI:
# build/tools/luajit (with lua51.dll on Windows), build/tools/tscnative.(so|dll) and
# build/tools/jit/ (LuaJIT's Lua modules, for -jv, -jdump and jit.p).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
if pwd -W >/dev/null 2>&1; then
	ROOT="$(cd "$ROOT" && pwd -W)"
fi
CC="${CC:-gcc}"
MAKE="${MAKE:-$(command -v make || command -v gmake || command -v mingw32-make)}"
BUILD="$ROOT/build/luajit-dev"
SRC="$BUILD/src"
TOOLS="$ROOT/build/tools"
# Lua 5.2 compatibility adds table.pack/unpack, package.searchers and __pairs, which the runtime uses.
XCFLAGS="-DLUAJIT_ENABLE_LUA52COMPAT"

# LuaJIT builds in its source tree, so build a copy and keep vendor/ clean.
rm -rf "$BUILD"
cp -r "$ROOT/vendor/luajit" "$BUILD"
mkdir -p "$TOOLS"
rm -rf "$TOOLS/jit"
case "$(uname -s)" in
	MINGW* | MSYS* | CYGWIN*)
		"$MAKE" -C "$SRC" -j8 CC="$CC" XCFLAGS="$XCFLAGS"
		cp "$SRC/lua51.dll" "$TOOLS/"
		# The interpreter is linked without MinGW's argument globbing ("?" in \\?\ paths matches files).
		"$CC" -O2 -I"$SRC" -o "$TOOLS/luajit.exe" "$SRC/luajit.c" "$ROOT/native/noglob.c" "$TOOLS/lua51.dll"
		"$CC" -O2 -shared $XCFLAGS -I"$SRC" -o "$TOOLS/tscnative.dll" "$ROOT/native/tscnative.c" "$TOOLS/lua51.dll"
		;;
	Darwin*)
		MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}" "$MAKE" -C "$SRC" -j8 CC="$CC" XCFLAGS="$XCFLAGS"
		cp "$SRC/luajit" "$TOOLS/luajit"
		"$CC" -O2 -bundle -undefined dynamic_lookup -I"$SRC" -o "$TOOLS/tscnative.so" "$ROOT/native/tscnative.c"
		;;
	*)
		"$MAKE" -C "$SRC" -j8 CC="$CC" XCFLAGS="$XCFLAGS"
		cp "$SRC/luajit" "$TOOLS/luajit"
		"$CC" -O2 -fPIC -shared -I"$SRC" -o "$TOOLS/tscnative.so" "$ROOT/native/tscnative.c" -ldl
		;;
esac
cp -r "$SRC/jit" "$TOOLS/jit"
echo "built $TOOLS"
