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
| D1 | In `Log()` the argument loop declares `err :=` inside the `if`, so the later `err = fmt.Errorf(...)` is lost. | A failed argument write is reported as success. | 8.1 |
| D2 | A handler records the argument types of the first call only. | A second call with the same address and other types is decoded wrongly. | 5 |
| D3 | File hash and line are 16 bits each. | Collisions between files, and no support for files over 65535 lines. | 5 |
| D4 | `ast.GetIndexTable` returns `nil, nil, nil`. | No offline decoding. | 5 |
| D5 | `ast` records `reflect.TypeOf(astNode)`, which is the AST node type. | The recorded types are meaningless. | 5 |
| D6 | `Fifo.WriteIntegral` writes one byte and ignores `count`. | The FIFO cannot hold 2, 4 or 8 byte values. | 6 |
| D7 | The stream flags are global variables and the stream is unversioned. | A stream cannot be decoded without knowing the writer's flags. | 4 |
| D8 | There is no `go.mod`, and `ast` imports `"binlog"`. | `go test ./...` does not run. | 3 |
| D9 | `Handler.Args.args` is indexed by `args` length checked at call time only. | Error on mismatch, but only after the handler is cached from a bad first call. | 4 |

## 3. Build and packaging (NFR-BUILD-1, NFR-PORT-1)

- Add `go.mod` with module path `github.com/larytet/binlog`.
- Replace the `"binlog"` import in `ast` by the module path.
- Pin the three `larytet-go/*` dependencies in `go.mod`. Vendor a replacement if one is gone.
- Rewrite `buildall.sh` and `testall.sh` to call `go build ./...` and `go test ./...`.
- Keep `firstmoduledata.s`. Add a test that fails with a clear message when the
  `firstmoduledata` link trick or the string-address assumption breaks on a new Go release.

## 4. Stream format v1 (FR-LOG-9, NFR-COMPAT-1, D7, D9)

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
| `float32` | `%f %e %g` | 4 bytes, IEEE 754 bits |
| `float64` | `%f %e %g` | 8 bytes, IEEE 754 bits |
| `rune` / `int32` | `%c` | 4 bytes |

## 5. Offline index table (FR-DEC-2 to FR-DEC-8, D2 to D5)

### 5.1 Pipeline

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

## 6. FIFO (FR-FIFO-1 to FR-FIFO-6, D6)

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

### 8.1 Error handling (D1)

Fix the shadowed `err` in `Log()`. Return the first write error and still call
`FrameEnd`, so that a shared writer is never left mid-frame.

### 8.2 Float and char (FR-LOG-7, FR-LOG-8)

Extend `parseLogLine` to accept `f e g c`, reuse `writerByteArray{count: 4|8}` for the raw
float bits (it already writes integers of those widths), and extend `DecodeNext` and
`appendArg` for the new kinds.

### 8.3 Decoder command (FR-DEC-10)

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
2. Error handling fix (8.1).
3. Float and char (8.2) with the stream header (4).
4. Offline index table (5) and the decoder command (8.3).
5. FIFO phase 1 (6).
6. FIFO phase 2, sorting, compression and the integrations, as time allows.
