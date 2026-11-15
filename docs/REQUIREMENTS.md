# binlog: Requirements Specification

Status: draft 1. Sources: `README.md` (About, Limitations, Todo) and the code as of commit `0c126cc`.

Requirement IDs are stable. The design spec, the test plan and the tests refer to them.
Priority: **M** = must, **S** = should, **C** = could.
Status: **Done** = in the code today, **Partial** = started, **Open** = not started.

## 1. Purpose and scope

`binlog` is a binary logging library for Go. A call such as `Log("fmt %d", x)` emits a
fixed-size hash of the constant format string plus the raw argument bytes. Formatting is
deferred to an offline decoder. The goal is minimal latency on the hot path.

In scope: the in-process logger, the byte stream format, the output FIFO, offline decoding
from the executable and sources, and export of decoded entries.

Out of scope: a network log transport, log rotation, and an interactive viewer.

## 2. Definitions

- **Format string**: the first argument of `Log()`.
- **Handler**: the cached metadata for one format string (argument types, verbs, hash, index).
- **L1 cache**: a lookup indexed by the string's address in the executable image.
- **L2 cache**: a map keyed by the string, for strings not in the image (heap-built).
- **Index table**: the map from hash to Handler that the decoder needs.
- **Entry**: one logged call in the stream.

## 3. Functional requirements

### 3.1 Logging API

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-LOG-1 | M | Done | `Log(fmt string, args ...interface{}) error` writes one entry to the configured `io.Writer`. |
| FR-LOG-2 | M | Done | A constant format string with no arguments writes only its 32-bit hash. |
| FR-LOG-3 | M | Done | Integer arguments of 8, 16, 32 and 64 bits, signed and unsigned, are written in their native width. |
| FR-LOG-4 | M | Done | String arguments are written length-prefixed. Strings of any length, including empty, round trip. The length prefix is 32 bits. |
| FR-LOG-5 | M | Done | `WriterControl.FrameStart/FrameEnd` is called around every entry so that writers can be shared. |
| FR-LOG-6 | S | Done | Optional per-entry fields: running index, string index, file hash and line, timestamp (`SEND_LOG_INDEX`, `SEND_STRING_INDEX`, `ADD_SOURCE_LINE`, `ADD_TIMESTAMP`). |
| FR-LOG-7 | M | Open | `float32` and `float64` arguments (`%f`, `%e`, `%g`) are supported. |
| FR-LOG-8 | S | Open | `char` / rune (`%c`) is supported. |
| FR-LOG-9 | M | Partial | An unsupported verb (`%v`, `%T`, `%p`) or argument type returns an error. It never corrupts the stream. |
| FR-LOG-11 | M | Done | `%%` in a format string is a literal percent sign and consumes no argument (D10). |
| FR-LOG-10 | S | Done | A format string not in the image is served from the L2 cache, with correct output. |

### 3.2 Caching

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-CACHE-1 | M | Done | An L1 hit costs two operations or fewer: a subtraction and an index. |
| FR-CACHE-2 | M | Done | A miss builds and stores the Handler; later calls hit. |
| FR-CACHE-3 | M | Done | `GetStatistics()` reports L1/L2 hits and misses. |
| FR-CACHE-4 | M | Done | Two distinct calls with the same constant text are detected, because string deduplication would break the L1 key. A test guards this. |

### 3.3 Concurrency

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-CONC-1 | M | Done | The documented model is one `Binlog` instance per goroutine or thread. |
| FR-CONC-2 | S | Open | Several loggers can share one output through a FIFO or `WriterControl` without interleaving inside an entry. |
| FR-CONC-3 | C | Open | A single `Binlog` can be used from several goroutines with one atomic on the hot path. |

### 3.4 FIFO writer (`io` package)

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-FIFO-1 | M | Open | `Fifo` supports writing and reading 1, 2, 4 and 8 byte values. Today it writes one byte and ignores `count`. |
| FR-FIFO-2 | M | Open | Allocation returns a contiguous block. If the space at the end is too small, the tail is marked skipped and allocation restarts at offset 0. |
| FR-FIFO-3 | M | Open | Each block has an allocated flag and an initialized flag. The consumer reads only initialized blocks, in allocation order. |
| FR-FIFO-4 | M | Open | When the FIFO is full, a write fails or drops and increments a counter. It never overwrites unread data. |
| FR-FIFO-5 | S | Open | Several producers can allocate concurrently with one consumer, using one atomic per allocation. |
| FR-FIFO-6 | M | Open | `Len()` reports the number of unread bytes correctly, including after wraparound. |

### 3.5 Offline decoding

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-DEC-1 | M | Done | `DecodeNext` decodes an entry from a stream given an index table. |
| FR-DEC-2 | M | Open | `ast.GetIndexTable(executable)` returns the real index table. Today it returns `nil, nil, nil`, and its input reader `moduledata.GetModules` crashes on current executables (D14). |
| FR-DEC-3 | M | Open | The decoder finds every `binlog.Log` call in the Go sources the executable was built from. It reads the module paths from the executable. |
| FR-DEC-4 | M | Open | Handlers use the hash that the runtime computes, so offline and online hashes match for the same string. |
| FR-DEC-5 | M | Open | Argument types come from type information, not from AST node types. `uint32(10)` and `uint64(10)` in two calls with the same text decode correctly. |
| FR-DEC-6 | M | Open | Calls are identified by file and line, so that duplicate format strings do not collide. |
| FR-DEC-7 | S | Open | An import alias or a method receiver other than `binlog` is resolved by type, not by name. |
| FR-DEC-8 | S | Open | Calls with an unsupported argument expression are reported with file and line, not dropped silently. |
| FR-DEC-9 | C | Open | Decoding from the ELF `.rodata` alone, without sources. |
| FR-DEC-10 | S | Open | A command-line decoder prints a binary stream as text, given a stream file and the executable. |

### 3.6 Ordering and compression

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-SORT-1 | C | Open | Entries from several loggers can be sorted by timestamp or index within a configurable window. |
| FR-COMP-1 | C | Open | Blocks of the stream can be compressed and decompressed, with the algorithm selectable. |

### 3.7 Integrations

| ID | Pri | Status | Requirement |
|---|---|---|---|
| FR-INT-1 | C | Open | Decoded entries can be exported as newline-delimited JSON for bulk load into Elasticsearch. |
| FR-INT-2 | C | Open | Megalog integration. The Megalog interface must be defined before this can be specified. |

## 4. Non-functional requirements

| ID | Pri | Requirement |
|---|---|---|
| NFR-PERF-1 | M | A constant string with no arguments costs about 15 ns per call, and no more than 25 ns on the reference machine. |
| NFR-PERF-2 | M | Each added integer argument adds about 10 ns for a constant and about 20 ns for a variable. |
| NFR-PERF-3 | M | The hot path allocates zero bytes per call on an L1 hit. |
| NFR-PERF-4 | S | The FIFO write path is no more than 10 ns slower than writing to a `bytes.Buffer`. |
| NFR-PORT-1 | M | The module builds with `go build ./...` and tests pass with `go test ./...` on current Go on linux/amd64. |
| NFR-PORT-2 | S | The code builds on linux/arm64. Windows and macOS are not supported, and the README states this. |
| NFR-BUILD-1 | M | A `go.mod` exists, with no GOPATH assumptions and no bare `"binlog"` import path. |
| NFR-SAFE-1 | M | The race detector reports nothing for the supported concurrency model. |
| NFR-COMPAT-1 | M | The stream format is versioned, or documented as unstable. A change to it needs a test change. |
| NFR-QUAL-1 | S | Statement coverage is 80% or higher per package. The README quotes 86.2%. |
| NFR-DOC-1 | M | The README matches the code, and its limitations and Todo sections are kept current. |

## 5. Constraints and assumptions

- String literals sit in one segment of the ELF image, and each literal has a unique address.
  If Go deduplicates literals, the L1 key must grow, which costs performance.
- The implementation uses `unsafe`, `reflect`, `StringHeader` and the `firstmoduledata` link
  workaround (golang/go#15006). These may break on a new Go release, so the build must detect it.
- The application flushes the output itself.
- The only non-standard dependency of the library is `larytet-go/moduledata` (until FR-DEC-2 lands). Benchmarks also use zap, logrus, glog and fasthttp.

## 6. Acceptance

A requirement is accepted when:

1. A test in the test plan references its ID and passes.
2. For NFR-PERF requirements, a benchmark result is recorded in the repository.
3. The README is updated where the requirement changes user-visible behaviour.

## 7. Open questions

1. What is Megalog, and is its interface public? FR-INT-2 is blocked on this.
2. Is a stream format change allowed before 1.0? This decides NFR-COMPAT-1.
3. Which Go versions must be supported? The `firstmoduledata` trick is version-sensitive.
4. Is FR-CONC-3 wanted, given the documented per-thread model?
