# tsc-lua runtime conventions

tsc-lua is produced by `golua` (in `transpiler/`), a Go to LuaJIT transpiler, applied to the
TypeScript 7 compiler (`upstream/TypeScript/tsc`). Generated modules run on the hand written
runtime in `runtime/rt.lua`. Packages that cannot be transpiled (or are too slow when
transpiled) have native Lua implementations in `runtime/natives/`.

## Modules

- Go package `strings` becomes Lua module `go.strings`, `golang.org/x/...` becomes `x....`,
  `github.com/microsoft/TypeScript/tsc/internal/checker` becomes `ts.checker`.
- Every module builds a package table `P = rt.package(path)` and returns it. Imports use
  `rt.import(modname)`, which `require`s the module and runs its `P.__init` (package variable
  initialization and `init` functions) once.
- Package level functions are `P.Name`, methods are `P.Type__Method` (receiver first),
  package variables are `P.name` (address-taken scalars are boxes: `P.name.v`).
- A file `runtime/natives/<module path>.lua` fully replaces a package. A file
  `runtime/natives/<module path>_patch.lua` returns `function(P)` that is called after the
  transpiled functions are defined, to override some of them.
- A native module lists transpiled packages it needs on a `-- deps: a/b c` line so the
  transpiler emits them.
- Small functions are inlined at static call sites, which would bypass a patch. A patch
  names the functions it replaces on a `-- overrides: Name Type__Method` line; without that
  line, nothing in the patched package is inlined.
- A type declared inside a function is built once per module and kept in the module's `LT`
  table (once per instantiation, keyed by the type arguments' ids, when it uses the enclosing
  generic function's type parameters). Building it on every call made a new type per call and
  kept every one of them alive.
- A `switch` over eight or more integer constants is emitted as a binary search on the tag that
  jumps (`goto`) to the case body; bodies follow in source order, so `fallthrough` runs on into
  the next one.
- Small functions are inlined as expressions: a single return, or guards of the form
  `if cond { return x }` / `if cond { panic("...") }` before it, become `cond and x or rest`
  (only where `x` cannot be Lua-falsy). Templates that call functions or name module tables
  (`P`, `TY`, import aliases) apply only within their package, with simple arguments, and their
  `TY` slots and aliases are remapped into the calling module.
- A function literal that captures nothing from its function is defined once, at module level
  (the `FN` table). A literal that captures variables is a callable table: its slots hold the
  captured values (or boxes, for variables assigned after their declaration, which are then
  boxed everywhere) and its metatable's `__call` is a module-level function taking that table
  first (`rt.closure`). LuaJIT's JIT cannot compile closure creation (FNEW) or the upvalue
  closing (UCLO) that every return of a function containing a closure performs, and this keeps
  closures out of the compiled code. Literals capturing a struct or array variable that is
  reassigned stay Lua closures. The body of a range-over-func loop (the yield function) is
  converted the same way, its table also carrying the control signal and results that `break`,
  `continue` and `return` pass out; bodies with `defer` or `goto` stay closures, and ranging over
  `slices.Backward` is a plain reverse loop. Function-local type descriptors a converted function
  uses are captured too. Method values (`rt.bind`), deferred calls (`rt.thunk`) and the
  string and map iterators are callable tables or stateless for the same reason.
- A function with `defer` runs its body as a module-level function of the defer list and its
  parameters, under `xpcall`, and `rt.finish` runs the deferred calls and returns the body's
  results (or the zero values after a recovered panic). Functions with named results, whose
  deferred calls may change them, keep the body in a closure.
- `s = append(s, ...)` and `s = s[i:j]` update the header in place (`rt.push1`,
  `rt.reslice`, ...) when s is a slice field or local whose header nothing else can hold
  (transpiler/inplace.go); struct copies duplicate slice headers so they never share one.
- Capacity that append grows into is left nil, not zeroed. A slice expression with a high bound
  passes the element's zero (`slice(s, lo, hi, z)`, nothing for nilable elements), and reaching
  past len fills the nil slots it exposes.
- Struct-valued fields, named as well as embedded, are flattened into the containing table when
  their keys do not collide (`Node.Loc`'s `pos` and `end` live on the node itself). A
  flattened named field keeps flags `0` and has `true` as the sixth element of its field
  descriptor. Native types are never flattened. Fields of the `sync/atomic` integer types hold
  the plain value; their common methods compile to `rt.aadd` and friends, the others get a field
  pointer.
- A Go substring is free and Lua's `string.sub` copies, so substrings are kept as views where
  that matters (transpiler/strview.go, transpiler/views.go). A string local initialized from
  `s[i:j]` is a `(base, offset, length)` triple of locals, and `utf8.DecodeRuneInString`,
  `strings.Index` and similar calls on a substring read the base in place (`rt.decodeRuneAt`, ...).
  A string parameter that some caller passes a substring to, and that its function only measures,
  indexes, slices or passes on to another such parameter, is a view parameter: the function takes
  the base string plus two extra trailing parameters (`name__o`, `name__n`), which callers with a
  whole string leave out. Functions a patch overrides never get view parameters.
- A capped string field (listed in transpiler/views.go: the scanner's `text` and the parser's
  `sourceText`) can be cut to a prefix of itself without a copy: the slot keeps the whole string
  and the key `<field>__n` holds the shorter length, nil when the field is not cut. `len`, indexing
  and slicing read through the pair; reading the field as a plain value makes the cut real
  (`rt.capstr`) and any other store clears the length. The JSDoc parser cuts the source text at
  the end of every comment it parses, which copied most of the file each time.
- Structs of the module being compiled are indexed: fields live in the table's array part
  (descriptor key `1`, `2`, ...; code reads `x[3]`), which costs 8 bytes a field where a hash
  entry costs 24 and rounds up to a power of two. Each struct type has one slot range wherever
  it is stored, and types that share a table (flattened into the same struct) get ranges that
  do not overlap, so a table can have unused (nil) slots (transpiler/layout.go). Constructors
  list slots positionally so they land in the array part. Go library structs keep string keys,
  since natives read them by name; patches find a compiler struct's slots with `rt.fieldkey`.

## Values

| Go                                   | Lua                                                                  |
| ------------------------------------ | -------------------------------------------------------------------- |
| `bool`, `string`                     | boolean, string (Go strings are byte strings, so are Lua strings)    |
| `int`                                | number, exact below 2^53 (no wraparound at 2^64)                     |
| `int64`, `uint64`, `uint`, `uintptr` | `int64_t` / `uint64_t` FFI integer (exact, wraps like Go); named ID types listed in `narrow64` (transpiler/numbers.go) are numbers |
| `int32`, `uint32`                    | number holding the signed 32-bit pattern, as LuaJIT's `bit` operations return it (unsigned compares and conversions normalize with `% 2^32`) |
| `int8`, `int16`, `uint8`, `uint16`   | number, truncated to the width after arithmetic                      |
| `float64` / `float32`                | number (`float32` rounded through an FFI `float`)                    |
| struct value                         | table, fields in array slots (structs of the compiled module) or keyed by name; struct fields are flattened into the same table when their keys do not collide |
| `*Struct`                            | the same table (so `&s == s` and casts between a struct and its first embedded struct are identity) |
| `*T` for non-struct `T`              | box `{v = value}`, or a proxy with the same `.v` shape for `&slice[i]` / `&s.field` |
| array `[N]T`                         | 1-based table of N elements                                          |
| slice                                | `{backing table, 1-based offset, len, cap}` by position; nil slice is `nil`; element `i` is `s[1][s[2] + i]` |
| map                                  | table whose entry count lives at key `rt.LEN`; nil map is `nil`      |
| map with struct/array/interface keys | the Lua key is a serialization and the entry is `{key, value}`       |
| `chan`                               | runtime channel object                                               |
| func                                 | Lua function; multiple results are Lua multiple returns              |
| variadic `...T`                      | received as a slice                                                   |

Map values that may be `nil` are stored as `false` (pointers, slices, maps, funcs) or `rt.NIL`
(interfaces) so presence checks work; `rt.nn` converts back.

## Types and interfaces

Every type has a runtime descriptor (`rt.int`, `rt.ptrTo(T)`, `rt.sliceOf(T)`, named types are
`P.TypeName`, generic types are called with their type arguments: `P.OrderedMap(K, V)`).
Descriptors provide `kind`, `str` (Go spelling, used by `%T`), `zero()`, `clone(v)`, `copy(dst, src)`,
`eq(a, b)`, `box(v)`, `new()`, `mapkey(v)`, and for structs `fields`
(`{key, type, name, flags, tag}`; flags 2 = flattened embedded struct, 1 = embedded, 0 = field).

Interface values:

- `bool`, `string` and `int` (the unnamed types) are stored raw. `float64` is wrapped, since int and
  float64 values are both Lua numbers.
- A pointer to a struct is stored raw when the table's metatable is that struct's `pmt`
  (it was allocated as that type); otherwise it is wrapped.
- A struct value is a copy whose metatable is the struct's `vmt`.
- Everything else is a wrapper `{__t = descriptor, __v = value}`.

`rt.typeof(x)` returns the dynamic descriptor and `rt.unbox(x)` the underlying value.
Interface method calls dispatch through metatables with keys `_m<Name>`: `x:_mString()`.
`rt.methods(T, valueSet, pointerSet)` installs dispatch tables, and `T.mset` lists the methods.

## Generics

Type parameters are passed as descriptors before the regular arguments, including on methods of
generic types: `P.Map(T, U, xs, f)`, `P.OrderedMap__Set(K, V, m, k, v)`.

## Control flow

- `panic(v)` raises a Lua error wrapping `v`; functions with `defer` run their body under `xpcall`
  and `rt.rundefers` executes the deferred calls, where `rt.recover()` stops the panic.
- Go func values are Lua functions or callable tables (see above); code only ever calls them.
- Goroutines are coroutines on a cooperative scheduler (`rt.go`, `rt.park`, `rt.ready`);
  `rt.runmain` drives it. Channels, `select`, and `sync` primitives park and wake goroutines.
- `os.Exit` raises `rt.ExitError`, handled at the top level.

## Checking in copies of the process

Go checks a program's files on several threads. Lua has one, so a plain `--noEmit` compilation
shares the checking with copies of the process (`runtime/natives/ts/checkcopies.lua`, hooked in
by the `compiler` and `execute` patches):

- The program is parsed and bound once, with a single checker (`--checkers` is set to 1). The
  process is then copied with `tscnative.fork`: `fork` where it exists, `RtlCloneUserProcess`
  on Windows. A copy shares all the memory of the original until either writes to it, so it
  starts with the whole program, the checker and the compiled traces, at no cost.
- The files are divided into as many runs of consecutive files as there are processes. The
  original checks the first run and each copy one of the others, carrying on from the same
  checker. Consecutive files matter: neighbours import each other, so a process that checks
  neighbours repeats less of the type resolution the other processes also need.
- A copy writes what it found to a file (a Lua literal per line: each file's diagnostics and
  declaration diagnostics, and the checker's global diagnostics and counters) and exits with
  `tscnative.exit`, without flushing anything it inherited. The original reads the files.
- LuaJIT's collector writes to every object it visits, which would copy all the memory the
  processes share. So the compilation collects only young objects (see below): each process
  frees its own garbage and leaves what it inherited alone. A copy still stops taking files
  when the machine runs low on memory.
- Nothing is freed before the copies are made, unless the heap outgrows a quarter of the
  memory that was free. Freed objects leave holes all over the heap, and every copy would
  fill them with its own objects, which unshares those pages (it cost 1 to 2 GB on the
  benchmark apps). Just before copying, everything that exists is made old, garbage included.
- Before anything depends on it, a throwaway copy is made while the heap is small. If it does
  not allocate, write a file and exit within ten seconds, the compilation checks in one
  process. Copies are not tried under ARM64 emulation on Windows.
- Whatever no copy reported is checked by the original afterwards, so a copy that crashed, was
  killed, stopped early or wrote something unreadable only costs time.
- Copies are not used when the compilation emits, is incremental, watches, builds (`-b`),
  traces, is single threaded or names `--checkers`, when a profiler is running, or when
  `TSCLUA_CHECK_PROCESSES=1`. `TSCLUA_CHECK_DEBUG=1` reports what each process did.
- A copy never outlives the original: on Windows the copies are in a job object that ends with
  it, elsewhere a thread in each copy watches for its exit. On Windows a copy is not known to
  the Windows subsystem, so it must not use the console or start threads.

### Collecting only young objects

`collectgarbage('young', kilobytes)` is an addition to the LuaJIT in `vendor/` (`lj_gc.c`). From
then on a collection runs every time that much was allocated and looks only at objects made
since the previous one. An object that survives a collection is old: it is never freed, and a
collection never writes to it unless the program stored into it. Calling it again makes
everything that exists old without freeing anything, and sets the new size.

- The collector stays in its propagation phase for good. LuaJIT's write barriers then do what
  a generational collector needs: a store into an old table puts the table on a list to look
  at again, and a store into any other old object marks the young object at once.
- Thread stacks have no barrier, so every thread is looked at in every collection.
- The sweep walks the root list from its head, where new objects are added, to the first old
  object. Strings live in the string table instead, so the ones made since the last
  collection are kept in a vector.
- A collection never runs on a trace: the trace exits first, as for LuaJIT's atomic phase.
- Garbage that was already old stays. A compilation promotes little that dies later (tRPC's
  heap ends 4% larger than after a full collection), but a long-running process should not
  use this mode. `TSCLUA_GC_YOUNG=<megabytes>` turns it on from the start, for testing it.

## Natives

Native packages follow the same conventions as generated code so both can call each other,
including the number representations: a native returning an `int64` returns an FFI integer.
Lua patterns cannot contain a NUL byte under LuaJIT; write `%z`.
Names declared in Go but not exported keep their Go spelling. Keep natives small: prefer
transpiling a package and patching a few functions over rewriting it.
