#!/usr/bin/env bash
# Clones microsoft/TypeScript at the commit pinned in upstream.lock into upstream/TypeScript.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMMIT="$(tr -d '[:space:]' < "$ROOT/upstream.lock")"
DEST="$ROOT/upstream/TypeScript"
if [ -d "$DEST/.git" ] && [ "$(git -C "$DEST" rev-parse HEAD)" = "$COMMIT" ]; then
	echo "upstream/TypeScript already at $COMMIT"
	exit 0
fi
if [ -e "$DEST" ]; then
	echo "upstream/TypeScript exists at a different commit; remove it to fetch $COMMIT" >&2
	exit 1
fi
git clone -q --depth 1 --revision "$COMMIT" https://github.com/microsoft/TypeScript.git "$DEST"
echo "upstream/TypeScript at $COMMIT"
