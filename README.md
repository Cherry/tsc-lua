# tsc-lua

[![npm version](https://img.shields.io/npm/v/tsc-lua)](https://www.npmjs.com/package/tsc-lua)
[![CI](https://github.com/Cherry/tsc-lua/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/Cherry/tsc-lua/actions/workflows/ci.yml)
[![license](https://img.shields.io/npm/l/tsc-lua)](./LICENSE)

The TypeScript 7 compiler (`tsc`), ported to Lua and run on LuaJIT 2.1.

TypeScript 7 is the native port of the TypeScript compiler, written in Go. tsc-lua runs that exact compiler as Lua: the Go sources are translated by a purpose built Go to Lua transpiler (`transpiler/`), and run on a small Lua runtime that implements Go's semantics (`runtime/`). Because it is the same compiler, it reports the same diagnostics and emits the same output as `tsc` from TypeScript 7, and it is checked against TypeScript's own baseline test suite.

This is a for-fun project. It is much slower than the Go compiler; see [Benchmarks](#benchmarks).

> [!NOTE]
> **Heads-up:** this project was primarily AI-authored. The transpiler, runtime, tooling, and this README were written by AI coding agents in interactive sessions, with a human operator directing the work, reviewing output, and validating it against TypeScript's conformance suite and real projects. Read the code before trusting it in production.

## Status

- **Same results.** All 13,727 configurations of TypeScript's compiler and conformance tests pass, comparing every baseline (errors, emitted JavaScript, declarations, source maps, types and symbols) byte for byte. See [Conformance](#conformance).
- **Real projects.** On the [benchmark apps](#benchmarks) tsc-lua reports the same diagnostics as `tsc` 7.
- **Slower.** About 33 times slower than `tsc` 7 on real projects, and it needs about 3.2 times the memory. See [Benchmarks](#benchmarks).

tsc-lua is pinned to [microsoft/TypeScript@2e9fb00](https://github.com/microsoft/TypeScript/commit/2e9fb00f5f5c9186d09aabd78cced5ce2497456d) (2026-10-17), the commit `typescript@7.1.0-dev.20261017.1` was built from. `tsc-lua --version` prints `7.1.0-dev`. When tsc-lua and that `typescript` build disagree, that is a tsc-lua bug; when they agree, it is upstream TypeScript behavior.

## Install

```sh
npm install -g tsc-lua
tsc-lua --version
tsc-lua -p tsconfig.json
```

The npm package installs a standalone executable for your platform through optional dependencies, the same way esbuild does. If your package manager skips optional dependencies, a postinstall script downloads the executable from the npm registry instead.

| Platform | Package |
| --- | --- |
| Windows x64 (arm64 runs it under emulation) | `@tsc-lua/win32-x64` |
| macOS x64, arm64 | `@tsc-lua/darwin-x64`, `@tsc-lua/darwin-arm64` |
| Linux x64, arm64 (glibc) | `@tsc-lua/linux-x64`, `@tsc-lua/linux-arm64` |
| Linux x64, arm64 (musl, e.g. Alpine) | `@tsc-lua/linux-x64-musl`, `@tsc-lua/linux-arm64-musl` |

The executable is LuaJIT with the compiler and the TypeScript lib files embedded; nothing else is needed at runtime. Set `TSC_LUA_BINARY_PATH` to use another executable.

`tsc-lua` accepts the same command line as `tsc`, including `-b` and `--extendedDiagnostics`. The language server (`--lsp`) and API (`--api`) modes are not included, and `--watch` is untested.

## Known differences

- **Single threaded.** Goroutines run as coroutines on one thread, so work that Go spreads over CPU cores runs one piece at a time. The work is still split the same way (for example between `--checkers`), so the output does not change.
- **Timing dependent output.** Where Go's output can depend on goroutine timing (for example `tsc -b` without project references reading sibling outputs), tsc-lua always gives the same one of Go's answers.
- **`--extendedDiagnostics`.** "Memory used" is the Lua heap, and "Memory allocs" counts Lua allocations, so neither is comparable to Go's numbers.
- **Local time zone.** Times shown by `-b --verbose` use the system's current UTC offset; a daylight saving change during a run is not picked up.
- **`int` overflow.** LuaJIT numbers are doubles, so Go's `int` is exact up to 2^53 and does not wrap around at 2^64. The compiler's integers (positions, counts, flags) stay far below that. The sized types, `int64`, `uint64` and `uint` keep Go's exact wraparound.

## How it works

```
upstream/TypeScript/tsc  (Go)  --transpiler-->  build/lua  (Lua for LuaJIT)  +  runtime/  -->  tsc-lua executable
```

- **Transpiler** (`transpiler/`): loads the Go packages with `go/types` and emits one Lua module per package. Go semantics are preserved: integer widths and wraparound, struct value semantics, embedded structs, interfaces and type switches, generics (type descriptors are passed at runtime, including Go 1.27 generic methods), closures with per-iteration loop variables, `defer`/`panic`/`recover`, goroutines and channels (coroutines), maps with any comparable key, and range-over-func iterators. It also optimizes for Lua: small functions and accessors are inlined, and Go substrings are represented as views to avoid copying.
- **Built for LuaJIT's compiler**: 32-bit integer types use LuaJIT's bit operations directly and 64-bit ones are FFI integers. Go function literals and range-over-func loop bodies become callable tables rather than Lua closures, and functions with `defer` run their body as a module-level function, since LuaJIT's JIT cannot compile code that creates closures.
- **Compact values**: the compiler's structs keep their fields in Lua array slots instead of named keys, each struct type at the same slots wherever it is stored, with nested structs flattened into the same table, and slice headers are 4-slot arrays. An AST identifier takes about 120 bytes instead of 300, which cuts both memory and garbage collection time.
- **Runtime** (`runtime/rt.lua`): type descriptors, interface boxing and dispatch, slices, maps, strings, a cooperative goroutine scheduler and channels. The representation is described in [docs/RUNTIME.md](docs/RUNTIME.md).
- **Natives** (`runtime/natives/`): Lua implementations of Go packages that cannot or should not be transpiled (`fmt`, `os`, `reflect`, `encoding/json/v2`, `sync`, `xxh3`, ...), and small patches of transpiled packages.
- **Executable** (`native/`): a C host embedding LuaJIT, a small OS module (`tscnative`: Unicode and long path file access on Windows, directory listing, stat and clocks) and an archive of the precompiled LuaJIT bytecode and lib files.

## Conformance

tsc-lua runs typescript-go's own compiler test runner (`internal/testrunner`), transpiled to Lua, over TypeScript's compiler and conformance test cases. Every generated baseline is compared with the reference baselines byte for byte.

| Baseline | What is compared | Passing |
| --- | --- | --- |
| `error` | Diagnostics, as printed by `tsc` | 13,727 / 13,727 |
| `output` | Emitted JavaScript and declarations | 13,653 / 13,653 |
| `sourcemap`, `sourcemap_record` | Source maps and their decoded mappings | 13,727 / 13,727 each |
| `type`, `symbol` | The type and symbol of every expression and name | 13,020 / 13,020 each |
| `union_ordering` | Order of union members | 13,727 / 13,727 |
| `content_mapper` | Content mapper (embedded file) handling | 13,727 / 13,727 |
| `source_file_parent_pointers` | AST parent pointers | 13,727 / 13,727 |
| `module_resolution` | Module resolution traces | 162 / 162 |
| **All** | **13,727 test configurations, 122,217 baselines** | **100%** |

A test configuration is a test file with one combination of its `// @option: a, b` variations. 1,728 configurations are skipped exactly as typescript-go skips them (options TypeScript 7 removed, such as `module: umd`). The full run takes about half an hour on 10 processes: `node scripts/conformance.ts`.

The transpiler has its own tests (`transpiler/testdata/`): Go programs whose output under tsc-lua must match `go run`.

[pingdotgg/ts-rust](https://github.com/pingdotgg/ts-rust)'s bug reports were checked too: each one that could affect a port was reproduced and compared with Go `tsc` at the same commit.

## Benchmarks

Seconds for a full type check (`--noEmit`), median of 5 runs after a warm up (tsc-lua: median of 3). Every compiler reported 0 errors on every app, the same as `tsc` 7.

| App | tsc 6 | tsc 7 | tsc-rs | tsc-rs `--fast` | bun check | tsc-ts | tsc-lua |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| tRPC | 2.91 | 0.46 | 0.29 | 0.29 | 0.21 | 0.49 | 12.3 |
| TypeORM | 11.29 | 1.47 | 0.87 | 0.71 | 0.40 | 1.56 | 43.8 |
| Excalidraw | 14.13 | 1.97 | 1.50 | 1.71 | 0.52 | 1.87 | 97.5 |
| Playwright | 12.05 | 1.67 | 0.85 | 0.73 | 0.50 | 1.46 | 52.5 |
| **vs tsc 7 (geometric mean)** | **7.1× slower** | **1×** | **1.6× faster** | **1.7× faster** | **3.2× faster** | **1×** | **33× slower** |

```
tsc-lua      ████████████████████████████████████████  33×
tsc 6        █████████                                 7.1×
tsc 7        ▏                                           1×
tsc-ts       ▏                                           0.98×
tsc-rs       ▏                                           0.62×
tsc-rs fast  ▏                                           0.58×
bun check    ▏                                           0.31×
```

Peak memory in MB (the largest peak working set of the compiler's processes, from the warm up run):

| App | tsc 6 | tsc 7 | tsc-rs | tsc-rs `--fast` | bun check | tsc-ts | tsc-lua |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| tRPC | 445 | 288 | 203 | 193 | 220 | 269 | 865 |
| TypeORM | 881 | 964 | 507 | 599 | 273 | 650 | 2,313 |
| Excalidraw | 1,061 | 1,034 | 603 | 896 | 380 | 1,028 | 4,893 |
| Playwright | 1,181 | 1,070 | 694 | 1,030 | 418 | 1,074 | 3,358 |
| **vs tsc 7 (geometric mean)** | **1.1×** | **1×** | **0.61×** | **0.77×** | **0.42×** | **0.89×** | **3.2×** |

Measured on Windows 11 with an AMD Ryzen 7 3700X (8 cores, 16 threads) and 32 GB of RAM, on 2026-10-18. Versions: `typescript` 7.0.2 and 6.0.3 (Node.js 26.12.0, `--max-old-space-size=16384`), `tsc-rs` 0.7.0, `tsc-ts` 0.0.3, bun 1.4.4, and tsc-lua at the pinned commit. tsc-lua runs on one core while the others use all of them; most of the gap is Lua itself, and the rest is that parallelism.

Lua tables cost more than Go structs, and LuaJIT's garbage collector is incremental only (not generational), so it lets garbage build up further before collecting. The collector settings trade speed for memory: on tRPC, `TSCLUA_GC_PAUSE=175 TSCLUA_GC_STEPMUL=600` takes about 6% longer for an 11% lower peak, and `TSCLUA_GC_PAUSE=125 TSCLUA_GC_STEPMUL=800` about 45% longer for 25% less. Storing struct fields and slice headers in array slots and keeping closures out of hot loops (which LuaJIT's JIT cannot compile) made tsc-lua about 30% faster and cut Excalidraw's peak from 7.6 GB to 4.9 GB.

Not measured yet: [tsrs](https://www.npmjs.com/package/tsrs) has no Windows build, and the Sentry and VS Code apps, which take tsc-lua well over an hour each.

### Method

The apps and commits are the ones [pingdotgg/ts-rust](https://github.com/pingdotgg/ts-rust) benchmarks, so the numbers are comparable. `scripts/bench/setup.sh` downloads each app as a source archive at its commit, installs its dependencies without running install scripts, and applies the same fixes as ts-rust so that each config type checks with no errors under `tsc` 7:

| App | Commit | Config | Preparation |
| --- | --- | --- | --- |
| [tRPC](https://github.com/trpc/trpc) | [`d756e59`](https://github.com/trpc/trpc/tree/d756e591a5e37ef20b8d75ecd4d736c195497289) | `packages/server/tsconfig.json` | |
| [TypeORM](https://github.com/typeorm/typeorm) | [`c64a1f0`](https://github.com/typeorm/typeorm/tree/c64a1f052fc39f6688b6b73b83d065d7147ba8bb) | `packages/typeorm/tsconfig.json` | `module` and `moduleResolution` set to `nodenext` |
| [Excalidraw](https://github.com/excalidraw/excalidraw) | [`53973c3`](https://github.com/excalidraw/excalidraw/tree/53973c3a423fbd75a4ce68107786b4fcb90e4968) | `tsconfig.json` | `baseUrl` removed |
| [Playwright](https://github.com/microsoft/playwright) | [`d469960`](https://github.com/microsoft/playwright/tree/d469960fdfc461e2d5795a3fa48a58a52a91ecaf) | `tsconfig.json` | `node utils/generate_injected.js` |
| [Sentry](https://github.com/getsentry/sentry) | [`8294650`](https://github.com/getsentry/sentry/tree/8294650589dbd26f230c73f4ab26b62a68aede8f) | `tsconfig.json` | |
| [VS Code](https://github.com/microsoft/vscode) | [`3f07e1a`](https://github.com/microsoft/vscode/tree/3f07e1aba32acacb8b08ae91bfdc954b580ad1fd) | `src/tsconfig.json` | Electron 43 typings installed |

`scripts/bench/run.ts` runs every compiler with `-p <config> --noEmit --incremental false --pretty false` (bun: `bun check` with the same options), with default thread counts. Each time is the median of 5 runs after a warm up run (tsc-lua: `--slow-runs 3`). The error count of each run is reported next to its time, so a faster tool that checks less is visible. Peak memory comes from the warm up run: the largest process's peak working set on Windows (`scripts/bench/peak.ps1`, which follows child processes such as the native compiler behind `tsc` 7's Node.js launcher) and peak RSS from `/usr/bin/time` elsewhere. Raw times, memory and diagnostics are written to `<work dir>/results`.

```sh
bash scripts/bench/setup.sh ../tsc-lua-bench
node scripts/bench/run.ts ../tsc-lua-bench --apps trpc,typeorm --runs 5
```

## Development

Requirements: Go 1.27, a C compiler (gcc or clang), make and Node.js 24. LuaJIT is built from `vendor/luajit` (LuaJIT 2.1 with its Lua stack limit raised to a million slots).

```sh
bash scripts/fetch-upstream.sh       # microsoft/TypeScript at the commit in upstream.lock
bash scripts/build-dev-lua.sh        # LuaJIT + tscnative in build/tools
bash scripts/transpile.sh            # Go -> Lua into build/lua
build/tools/luajit runtime/run.lua build/lua tsc.cmd.tsclua --version
bash scripts/test-transpiler.sh      # transpiler test programs, compared with `go run`
node scripts/conformance.ts          # TypeScript baseline tests (--run 'X/^name' for a subset)
bash scripts/build-binary.sh         # standalone executable in build/bin
npm run lint                         # ESLint for TypeScript, scripts/lint-lua.ts + StyLua for Lua
```

The transpiler is only ever run with `go run`; do not leave built transpiler executables around, as some antivirus software flags them.

Environment variables for investigating a run with `runtime/run.lua`:

| Variable | Effect |
| --- | --- |
| `TSCLUA_PROFILE=1` | Prints a sampling profile (LuaJIT's profiler, every millisecond) by VM state, function and line |
| `TSCLUA_PROFILE_TOP=60` | How many functions and lines the profile lists |
| `TSCLUA_TRACE_PANICS=1` | Prints Go panics with Lua tracebacks |
| `TSCLUA_MEMLOG=30` | Prints the Lua heap size every 30 seconds of CPU time |
| `TSCLUA_HEAPCENSUS=500` | When the heap first passes 500 MB, prints which Go types fill it |
| `TSCLUA_MEMLIMIT_MB=12288` | Allocations past the limit fail, which panics in the running goroutine (the conformance runner sets 12 GB, so a runaway test fails instead of exhausting memory). It counts garbage not yet collected too |
| `TSCLUA_GC_PAUSE=200` | LuaJIT's GC pause (200, LuaJIT's default, starts a cycle when the heap has doubled); higher is faster and uses more memory |
| `TSCLUA_GC_STEPMUL=400` | LuaJIT's GC step multiplier; 400 is the default here, against LuaJIT's 200, for a lower peak at the same speed |
| `TSCLUA_JIT=off` | Runs the interpreter only |
| `TSCLUA_JIT_OPT='hotloop=56 maxtrace=1000'` | Replaces tsc-lua's `jit.opt.start` parameters (it raises LuaJIT's trace limits and hot counts for a program this size) |
| `TSCLUA_CLOSE=1` | Closes the Lua state on exit, which LuaJIT's `-jp` profiler needs to write its report |

The executable reads the profile, panic tracing, GC and JIT variables too, but not `TSCLUA_MEMLOG`, `TSCLUA_HEAPCENSUS` or `TSCLUA_MEMLIMIT_MB`.

To move to a newer TypeScript, update `upstream.lock`, re-run `fetch-upstream.sh` and `transpile.sh`, and run the conformance tests.

### Windows

The development interpreter is linked without MinGW's argument globbing, and the executable reads its arguments from the UTF-16 command line, so paths with `?`, `*` and non-ASCII characters work. Use Git Bash for the shell scripts.

## License

[MIT](LICENSE). The executable includes TypeScript (Apache License 2.0, Microsoft), the Go standard library and modules (BSD style licenses) and LuaJIT (MIT, Mike Pall, including parts of Lua by Lua.org, PUC-Rio); their licenses are in [THIRD_PARTY_NOTICES.txt](THIRD_PARTY_NOTICES.txt), which `scripts/notices.sh` regenerates after `upstream.lock` changes.
