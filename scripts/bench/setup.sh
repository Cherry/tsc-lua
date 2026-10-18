#!/usr/bin/env bash
# Prepares the real-world app benchmark: comparison compilers and the apps at fixed commits
# with dependencies installed without install scripts. The app list, commits and per-app
# fixes follow pingdotgg/ts-rust's scripts/bench-apps/setup.sh so results are comparable.
# usage: scripts/bench/setup.sh <work-dir>
set -euo pipefail
[[ $# == 1 ]] || { sed -n '5p' "$0" >&2; exit 2; }
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
mkdir -p "$1" && cd "$1"
mkdir -p repos logs

# Comparison compilers, pinned so results are reproducible. Each must also pass the repo's npm
# policy (min-release-age); tsrs publishes only prereleases, which ranges never match.
[[ -f package.json ]] || echo '{ "private": true }' >package.json
cp "$ROOT/.npmrc" .npmrc
npm i -q --no-audit --no-fund typescript@7.0.2 ts6@npm:typescript@6.0.3 tsc-rs@0.7.0
npm i -q --no-audit --no-fund tsrs@0.14.1-ts7.1.0-dev.20260929 || echo "tsrs: not installable" >&2
npm i -q --no-audit --no-fund tsc-ts@0.0.0 || echo "tsc-ts: not installable" >&2
npm ls --depth 0 >logs/compilers.txt

checkout() { # name url sha
	[[ -d repos/$1 ]] && return
	# Source archives avoid git working tree operations entirely.
	mkdir -p "repos/$1"
	curl -fsSL "$2/archive/$3.tar.gz" | tar -xz -C "repos/$1" --strip-components=1
}
pnpm_for() { # project dir; prints a pnpm matching its packageManager pin
	local version
	version=$(node -p "((require('./$1/package.json').packageManager ?? '').match(/^pnpm@([^+]+)/) ?? [])[1] ?? 'latest'")
	# Installed per version outside the project: npx goes through npm, which rejects devEngines naming pnpm.
	if [[ ! -x "$PWD/pm/pnpm-$version/node_modules/.bin/pnpm" ]]; then
		mkdir -p "pm/pnpm-$version" && cp .npmrc "pm/pnpm-$version/" &&
			(cd "pm/pnpm-$version" && echo '{ "private": true }' >package.json && npm i -q --no-audit --no-fund "pnpm@$version") >&2
	fi
	echo "$PWD/pm/pnpm-$version/node_modules/.bin/pnpm"
}
install() { # name
	local dir="repos/$1"
	if [[ -f $dir/pnpm-lock.yaml ]]; then
		local pnpm
		pnpm=$(pnpm_for "$dir")
		(cd "$dir" && "$pnpm" install --frozen-lockfile --ignore-scripts)
	elif [[ -f $dir/yarn.lock ]]; then
		(cd "$dir" && npx -y yarn@1 install --frozen-lockfile --ignore-scripts)
	else
		(cd "$dir" && npm ci --ignore-scripts --no-audit --no-fund)
	fi
}
while read -r name url sha; do
	[[ ${BENCH_APPS:-all} == all || " ${BENCH_APPS:-all} " == *" $name "* ]] || continue
	checkout "$name" "$url" "$sha"
	(install "$name") >"logs/install-$name.log" 2>&1 || { echo "install failed: $name (logs/install-$name.log)" >&2; exit 1; }
done <<'EOF'
trpc https://github.com/trpc/trpc d756e591a5e37ef20b8d75ecd4d736c195497289
typeorm https://github.com/typeorm/typeorm c64a1f052fc39f6688b6b73b83d065d7147ba8bb
excalidraw https://github.com/excalidraw/excalidraw 53973c3a423fbd75a4ce68107786b4fcb90e4968
playwright https://github.com/microsoft/playwright d469960fdfc461e2d5795a3fa48a58a52a91ecaf
sentry https://github.com/getsentry/sentry 8294650589dbd26f230c73f4ab26b62a68aede8f
vscode https://github.com/microsoft/vscode 3f07e1aba32acacb8b08ae91bfdc954b580ad1fd
EOF

# The fixes that make each config check with 0 errors under tsc 7 (from ts-rust's setup).
if [[ -d repos/vscode && ! -d repos/vscode/node_modules/electron ]]; then
	mkdir -p tmp-electron && (cd tmp-electron && echo '{ "private": true }' >package.json &&
		npm i -q --ignore-scripts --no-audit --no-fund electron@43)
	cp -R tmp-electron/node_modules/electron repos/vscode/node_modules/electron && rm -rf tmp-electron
fi
if [[ -d repos/playwright ]]; then (cd repos/playwright && node utils/generate_injected.js); fi
if [[ -d repos/excalidraw ]]; then
	grep -v '"baseUrl"' repos/excalidraw/tsconfig.json >repos/excalidraw/tsconfig.bench.json
fi
if [[ -d repos/typeorm ]]; then
	echo '{ "extends": "./tsconfig.json", "compilerOptions": { "module": "nodenext", "moduleResolution": "nodenext" } }' \
		>repos/typeorm/packages/typeorm/tsconfig.bench.json
fi
echo ready
