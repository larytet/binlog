# binlog: Design Specification

Status: draft 1. Implements `docs/REQUIREMENTS.md`. Describes the code at commit `0c126cc`
(section 2) and the planned changes (sections 3 to 9).

## 1. Overview

```
 app goroutine                                   offline
 ┌──────────────┐   hash + raw args   ┌────────┐      ┌───────────────┐
 │ Binlog.Log() ├────────────────────►│ Writer │─────►│ stream file   │
 └──────┬───────┘                     └────────┘      └──────┬────────┘
        │ miss: build Handler                                │
        ▼                                     index table    ▼
  L1 (address) / L2 (string) cache  ·········►  ast.GetIndexTable ──► DecodeNext ──► text / NDJSON
```

Packages:

| Package | Role |
|---|---|
| `binlog` (root) | Hot-path logger, handler cache, stream format, `DecodeNext`. |
| `binlog/io` | `Fifo`, the output buffer. |
| `binlog/ast` | Offline index table builder (parses sources and the executable). |
| `cmd/binlogdump` (new) | Command-line decoder. |

## 2. Current design

### 2.1 Entry format

All integers are little endian. An entry is:

```
hash(4) [string index(4)] [file hash(2) line(2)] [log index(8)] [timestamp(8)] arg0 arg1 ...
```

The bracketed fields depend on the package-level flags `SEND_STRING_INDEX`, `ADD_SOURCE_LINE`,
`SEND_LOG_INDEX` and `ADD_TIMESTAMP`. The writer and the decoder must use the same flag
values, and the stream does not record them. Each argument is a native-width integer, or a
string as a length followed by bytes. The hash is the first 4 bytes of the MD5 of the format
string.

### 2.2 Hot path

`Log()` calls `getHandler()`:

1. `idx = (stringAddress - ConstDataBase) / 8`.
2. If `idx < ConstDataSize`, use `L1Cache[idx]`. This is the fast path.
3. Otherwise use the `L2Cache` map keyed by the string.
4. On a miss, `createHandler` parses the format (`parseLogLine`), takes the argument types
   from the first call's actual arguments, computes the hash, and registers the handler in
   `handlersLookupByHash`.

`Log()` then writes each part with a separate `io.Writer.Write`, each argument through a
writer chosen at handler creation (`writerByteArray`, `writerString`).

### 2.3 Known defects and limits

These drive the planned work.

| # | Observation | Effect | Planned in |
|---|---|---|---|
| D1 | In `Log()` the argument loop declares `err :=` inside the `if`, so the later `err = fmt.Errorf(...)` is lost. | A failed argument write is reported as success. | fixed |
| D2 | A handler records the argument types of the first call only. | A second call with the same address and other types is decoded wrongly. | fixed |
| D3 | File hash and line are 16 bits each. | Collisions between files, and no support for files over 65535 lines. | fixed |
| D4 | `ast.GetIndexTable` returns `nil, nil, nil`. | No offline decoding. | fixed |
| D5 | `ast` records `reflect.TypeOf(astNode)`, which is the AST node type. | The recorded types are meaningless. | fixed |
| D6 | `Fifo.WriteIntegral` writes one byte and ignores `count`. | The FIFO cannot hold 2, 4 or 8 byte values. | fixed |
| D7 | The stream flags are global variables and the stream is unversioned. | A stream cannot be decoded without knowing the writer's flags. | fixed (`WriteHeader`, `ReadHeader`, `DecodeNextFlags`) |
| D8 | There is no `go.mod`, and `ast` imports `"binlog"`. | `go test ./...` does not run. | 3 |
| D10 | `parseLogLine` sees `%%` and does `continue` without consuming the second `%`, so the next rune is read as a verb. | `Log("100%% sure %d", 7)` returns an error. | fixed |
| D11 | `DecodeNext` reads every integer as `uint64`, and `appendArg` switches on that value's type, not on the handler's `argType`. | `int8(-128)` decodes as 128 and `int(-10)` as 18446744073709551606. | fixed |
| D12 | `readStringFromReader` and `readIntegerFromReader` treat a zero-byte read as EOF. | An empty string argument fails to decode with "EOF". | fixed |
| D13 | The string length prefix is 16 bits. | A string of 70000 bytes is written truncated and decodes to the wrong text. | fixed |
| D14 | `moduledata.GetModules` dereferences a nil pointer on executables built by Go 1.27 (SIGSEGV, not recoverable). | `GetIndexTable` cannot be used on a current executable at all. | fixed |
| D15 | `parseLogLine` indexes `args[argIndex]` for each verb. | Fewer arguments than verbs panics (found by reading, not yet reproduced). | fixed |
| D16 | The L1 index is `address/8`, but literals are not aligned in the executable. Two short literals can share a slot. | The second call reuses the first one's handler and logs the wrong hash and arguments. Found when a layout change made `uint %d` and `int8 %d` collide. | fixed: a hit needs `Handler.Address` to equal the literal's address |
| D9 | `Handler.Args.args` is indexed by `args` length checked at call time only. | Error on mismatch, but only after the handler is cached from a bad first call. | 4 |

## 3. Build and packaging (NFR-BUILD-1, NFR-PORT-1)

Done: `go.mod` (Go 1.27.4, the latest release), module import paths. The legacy dependencies
`larytet-go/procfs`, `larytet-go/nanotime` and `larytet-go/sprintf` are replaced by the standard
library (`/proc/self/maps` parsing, `time`, `fmt`), and `reflect.StringHeader/SliceHeader` by
`unsafe.StringData/Slice`. `larytet-go/moduledata` remains until the offline decoder (section 5)
replaces it.

- Add `go.mod` with module path `github.com/larytet/binlog`.
- Replace the `"binlog"` import in `ast` by the module path.
- Pin the three `larytet-go/*` dependencies in `go.mod`. Vendor a replacement if one is gone.
- Rewrite `buildall.sh` and `testall.sh` to call `go build ./...` and `go test ./...`.
- Keep `firstmoduledata.s`. Add a test that fails with a clear message when the
  `firstmoduledata` link trick or the string-address assumption breaks on a new Go release.

## 4. Stream format v1 (FR-LOG-9, NFR-COMPAT-1, D7, D9) - header done

API: `(*Binlog).WriteHeader()` is called by the stream's owner once, `ReadHeader(r)` returns the `Flags`,
`DecodeNextFlags(r, table, files, flags)` decodes with them. `DecodeNext` keeps using the package
variables, so existing callers work. Entries cut inside the entry return `io.ErrUnexpectedEOF`, a clean
end is `io.EOF`. `testdata/golden_v1.bin` pins the byte layout.

Add a stream header, written once by `New` (or by the first `Log`):

```
magic "BLG1"(4)  flags(2)  reserved(2)
```

`flags` has one bit per optional field from 2.1. The decoder reads the header and no longer
depends on the package variables. Entries keep the layout in 2.1.

Handler creation validates the first call against the verbs and fails with an error for an
unsupported verb or type, so a bad first call is never cached (D9). A later call whose
argument count or kind differs from the handler's returns an error and writes nothing.

Argument encodings, added to the existing ones:

| Type | Verbs | Encoding |
|---|---|---|
| string length | `%s` | 4 bytes (done; was 2), then the bytes |
| `float32` | `%f %e %g` | 4 bytes, IEEE 754 bits |
| `float64` | `%f %e %g` | 8 bytes, IEEE 754 bits |
| `rune` / `int32` | `%c` | 4 bytes |

## 5. Offline index table (FR-DEC-2 to FR-DEC-8, D2 to D5) - implemented

Implemented in `ast/ast.go`: `GetIndexTable`, `LoadTable` (returns `Table` with `Handlers`, `Files`, `Sites`,
`Conflicts`, `Diagnostics`) and `LoadSources`. Differences from the plan below: the executable's file table is
read with `debug/gosym`; the stream's running string index is not known offline, so `DecodeNext` skips the index
check when the handler's `IndexUint` is zero; `Tests: true` is set so that calls in `_test.go` files are seen;
the runtime logger itself still does not detect same-text-different-type calls (D2 is detected offline only).

### 5.1 Pipeline

The module list comes from `debug/buildinfo` and the executable's DWARF or `runtime.pclntab`
file table (`debug/gosym`), read with the standard library. `moduledata.GetModules` is no longer
used (D14). If the executable has no file table (stripped), the decoder asks for the source
root with a flag instead of failing silently.

```
executable ──► moduledata.GetModules ──► Go files ──► packages.Load (go/types)
                                                          │
                                       walk CallExpr, resolve callee by types.Object
                                                          │
                       callee is (*binlog.Binlog).Log, first arg is a constant string
                                                          │
                                  per call: file, line, fmt string, []types.Type
                                                          │
                                  build Handler, hash = same md5sum as the runtime
```

### 5.2 Decisions

- **Resolve by type, not by name.** A call is a binlog call when the callee object is the
  method `Log` of `*github.com/larytet/binlog.Binlog`. This handles import aliases and
  wrapper variables. Name matching on `binlog.Log` is dropped (FR-DEC-7).
- **The format string is a constant.** Use `types.Info.Types[expr].Value` to evaluate
  constant expressions, such as concatenations of constants, not only `*ast.BasicLit`.
  A non-constant first argument is reported with file and line (FR-DEC-8) and skipped.
- **Types come from `types.Info`.** Each argument's static type is mapped to the handler's
  `decodeArg`. An argument of interface type is reported, since its dynamic type is unknown.
- **The hash must be shared code.** Export the format hash function from the root package
  and call it from `ast`, so that the two cannot drift (FR-DEC-4).
- **Duplicate strings.** Calls with the same constant text and the same argument types share
  one entry. Calls with the same text and different types are a conflict. The index table
  reports it, because the runtime hash cannot tell them apart (D2). The runtime path
  detects the same conflict at handler creation and returns an error. This is the cheaper
  and safer choice compared with widening the hash on the hot path.
- **File and line widths.** Replace the 16-bit file hash by a 32-bit one and keep 16 bits for
  the line only when `ADD_SOURCE_LINE` is set. Collisions are detected when the index table
  is built (D3).

### 5.3 Interface

```go
// package ast
func GetIndexTable(executable string) (map[uint32]*binlog.Handler, map[uint16]string, error)
type Conflict struct{ Hash uint32; Sites []Site }
func (t *Table) Conflicts() []Conflict
```

The existing signature stays so that callers keep working. The function returns an error
when the executable cannot be read, and returns a table plus a list of diagnostics when
some calls are skipped.

## 6. FIFO (FR-FIFO-1 to FR-FIFO-6, D6) - phase 1 implemented

Implemented in `io/io.go`. Differences from the plan: the ring size is a power of two (a mask instead of a
division); the 32 bit header packs the state and a 24 bit length and is the only synchronisation; positions are
monotonic 64 bit byte counters; the consumer stops at `head == tail` and never reads the memory at the tail (it
holds stale bytes of an earlier lap); `Fifo` is also an `io.Writer` and an `io.Reader`; `io.Framer` makes an
entry one block through the logger's `WriterControl`. Write cost is about 60 ns, see `docs/BENCH.md`.

### 6.1 Layout

A ring of `size` bytes, a producer `tail`, a consumer `head`, and blocks of the form:

```
state(1)  len(3)  payload(len)     padded to 8 bytes
```

`state` is `free`, `allocated`, `ready` or `skip`. Block starts are 8-byte aligned.

### 6.2 Operations

- `Alloc(n) (block []byte, ok bool)`: reserve `8 + roundup(n, 8)` contiguous bytes. If the
  space before the end of the ring is too small, write a `skip` block over it and restart
  at offset 0. Return false and count a drop if the ring would overtake `head`.
- `Commit(block)`: set `state = ready`. This is the only write the consumer depends on.
- `Read() (payload []byte, ok bool)`: consume the block at `head` if it is `ready`, step
  over `skip` blocks, and stop at an `allocated` block.
- `WriteIntegral(value, count)` and `ReadIntegral(count)` stay, now implemented on top of
  `Alloc`/`Commit` for 1, 2, 4 and 8 byte values.
- `Len()` is computed from the two offsets, corrected for wraparound.

### 6.3 Concurrency

Phase 1 is a single producer and a single consumer, with no atomics on the producer's
side except the final `state` store (a release store) and the consumer's `state` load
(an acquire load). Phase 2 (FR-FIFO-5) makes `tail` advance with one `atomic.AddUint64`
and relies on the `state` byte to order readers behind slow writers. Phase 2 is gated on a
benchmark meeting NFR-PERF-4.

### 6.4 Integration

`Config.IOWriter` accepts a `*io.Fifo` through a small adapter that satisfies `io.Writer`.
A faster path, `LogTo(fifo, ...)`, writes straight into the allocated block to avoid one
`Write` call per field. It is added only if the benchmark shows a gain.

## 7. Concurrency model (FR-CONC-1 to FR-CONC-3)

The default stays one `Binlog` per goroutine. Several loggers can share a FIFO in phase 2.
FR-CONC-3 is not designed here. It needs a decision on question 4 of the requirements first.

## 8. Smaller changes

### 8.1 Correctness fixes (D1, D10 to D13, D15)

- D10: after `%`, if the next rune is `%`, consume it.
- D11: make `appendArg` convert by the handler's `argType`, so signed kinds are sign-extended
  from their width.
- D12: allow zero-length reads in the string path; treat only a zero-byte read of the 4-byte
  hash at the start of an entry as end of stream.
- D13: widen the string length to 32 bits in stream format v1 (section 4).
- D15: check `argIndex < len(args)` and return an error.

### 8.1.1 Error handling (D1)

Fix the shadowed `err` in `Log()`. Return the first write error and still call
`FrameEnd`, so that a shared writer is never left mid-frame.

### 8.2 Float and char (FR-LOG-7, FR-LOG-8) - done

Extend `parseLogLine` to accept `f e g c`, reuse `writerByteArray{count: 4|8}` for the raw
float bits (it already writes integers of those widths), and extend `DecodeNext` and
`appendArg` for the new kinds.

### 8.3 Decoder command (FR-DEC-10) - done in `cmd/binlogdump`

`cmd/binlogdump <executable> <stream>` loads the index table, reads the stream header,
loops over `DecodeNext`, and prints `fmt.Sprintf(entry.FmtString, entry.Args...)`, with
the timestamp, index and source position when present.

## 9. Later work (could-have)

- **Sorting (FR-SORT-1)**: a decoder-side k-way merge over several streams by timestamp or
  index, with a bounded window.
- **Compression (FR-COMP-1)**: compress the FIFO's output per block in the consumer
  goroutine, never on the hot path. The block carries a length and an algorithm id.
- **Elasticsearch (FR-INT-1)**: `binlogdump -format ndjson` emits one JSON object per entry
  in the bulk API format.
- **Megalog (FR-INT-2)**: blocked until its interface is defined.

## 10. Risks

| Risk | Mitigation |
|---|---|
| A new Go release breaks the string-address trick. | The guard test in section 3. |
| `go/packages` needs the sources and a working `go` tool at decode time. | Document it. The ELF-only route (FR-DEC-9) stays a "could". |
| The FIFO phase 2 misses the performance target. | Keep phase 1 as the supported mode and drop phase 2. |
| A stream format change breaks old logs. | The header in section 4 carries a version. |

## 11. Implementation order

1. Build and packaging (3), because every later step needs `go test ./...`.
2. Correctness fixes and error handling (8.1), which also turn the `knownBroken` e2e cases green.
3. Float and char (8.2) with the stream header (4).
4. Offline index table (5) and the decoder command (8.3).
5. FIFO phase 1 (6).
6. FIFO phase 2, sorting, compression and the integrations, as time allows.
