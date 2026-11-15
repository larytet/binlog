# binlog: Test Plan

Status: draft 1. Covers `docs/REQUIREMENTS.md` and `docs/DESIGN.md`.

## 1. Strategy

Three levels, from cheap to expensive:

| Level | Where | What it proves |
|---|---|---|
| Unit | `*_test.go` next to the code | One function or type: parsing, writers, FIFO operations. |
| E2E (regression) | `e2e/` | The whole chain: build a program that logs, run it, decode its stream offline, compare with `fmt.Sprintf`. |
| Benchmark | `*_test.go`, `Benchmark*` | NFR-PERF requirements, recorded in `docs/BENCH.md`. |

The e2e level is the regression net. It is written before the features (task 4), is allowed
to fail for features that are not built yet, and each feature task turns its cases green.
Unsupported-yet cases are marked with `t.Skip("FR-xxx not implemented")` and the skip is
removed in the commit that implements the feature. A skipped test with no requirement ID is
not allowed.

### 1.1 Gates

- A task is committed and pushed only after `go build ./... && go vet ./... && go test ./...`
  passes. The result is captured to a file and checked, so a failure cannot be pushed by a
  chained command.
- Benchmarks are not part of the gate, because they are noisy. They run on request, and
  NFR-PERF checks compare against the recorded baseline with a 30% tolerance.
- `go test -race ./...` runs before every task that touches the FIFO or concurrency.

### 1.2 Environment

- linux/amd64, current Go. The string-address tests need a real executable, so they run as
  `go test` binaries and as the e2e programs built with `go build`.
- The e2e tests build the sample program with `-gcflags=all=-N -l` and again with default
  flags. Inlining can move string literals, and the logger must work in both.
- No network. The Elasticsearch export is tested as NDJSON text only.

## 2. Existing tests

| Test | Covers |
|---|---|
| `TestStringDedup`, `TestStringLocation*`, `TestGetStrings` | FR-CACHE-4, the string-address assumption |
| `TestInt`, `TestPrintIntegers`, `TestPrint2Ints`, `TestPrint` | FR-LOG-2, FR-LOG-3, FR-DEC-1 |
| `TestPrintString` | FR-LOG-4 |
| `TestL2Cache` | FR-LOG-10, FR-CACHE-3 |
| `TestReadme` | the README example |
| `ast.TestGetIndexTable` | currently only that the call does not crash (FR-DEC-2 is open) |
| `Benchmark*` (about 50) | NFR-PERF-1, NFR-PERF-2, comparisons with zap, logrus, glog, klog |

Known gaps: nothing covers FR-LOG-5 (frame callbacks), FR-LOG-6 flags in combination, FR-LOG-9
(unsupported verbs), D1 (the lost error), or anything in `io`. `io/io_test.go` is one line.

## 3. Test cases

IDs are `TC-<area>-<n>`. "Level" is U (unit), E (e2e), B (benchmark). "Task" is the
implementation task that makes the case pass. Cases for existing behaviour pass today.

### 3.1 Build and packaging

| ID | Level | Req | Case | Task |
|---|---|---|---|---|
| TC-BLD-1 | E | NFR-BUILD-1, NFR-PORT-1 | `go build ./...` succeeds in a clean checkout. | F1 |
| TC-BLD-2 | E | NFR-PORT-1 | `go test ./...` runs all three packages. | F1 |
| TC-BLD-3 | U | NFR-PORT-1 | Guard: a literal's address lies inside the range from `GetSelfTextAddressSize`, and two different literals have different addresses. Fails with a clear message otherwise. | F1 |

### 3.2 Logging

| ID | Level | Req | Case | Task |
|---|---|---|---|---|
| TC-LOG-1 | U | FR-LOG-2 | Constant string, no arguments: the stream is exactly the 4-byte hash. | now |
| TC-LOG-2 | U | FR-LOG-3 | Each of int8..int64 and uint8..uint64, with minimum, zero and maximum values, round trips. | now |
| TC-LOG-3 | U | FR-LOG-4 | Strings of length 0, 1 and 70000 bytes, and a string containing NUL and non-UTF-8 bytes, round trip. | now |
| TC-LOG-4 | U | FR-LOG-5 | A recording `WriterControl` sees `FrameStart`, the writes, then `FrameEnd`, once per call, also when the call returns an error. | F2 |
| TC-LOG-5 | U | FR-LOG-6 | Every combination of the four flags (16 cases) round trips. | now |
| TC-LOG-6 | U | FR-LOG-7 | `float32` and `float64` including 0, negative zero, NaN, +Inf and the largest and smallest values round trip bit for bit. | F3 |
| TC-LOG-7 | U | FR-LOG-8 | `%c` with ASCII, a multi-byte rune and the maximum rune round trips. | F3 |
| TC-LOG-8 | U | FR-LOG-9 | `%v`, `%T`, `%p` return an error and write no bytes. A wrong argument count or a wrong kind returns an error. | F2 |
| TC-LOG-9 | U | D1 | A writer that fails on the Nth `Write`: `Log` returns that error, and `FrameEnd` is still called. | F2 |
| TC-LOG-10 | U | D9 | A bad first call is not cached. A later correct call with the same string works. | F2 |
| TC-LOG-11 | U | FR-LOG-10 | A string built at run time uses the L2 cache and decodes correctly. | now |

### 3.3 Stream format

| ID | Level | Req | Case | Task |
|---|---|---|---|---|
| TC-FMT-1 | U | NFR-COMPAT-1 | A golden file (`testdata/golden_v1.bin`) decodes to the expected entries. A format change must change the golden file in the same commit. | F3 |
| TC-FMT-2 | U | D7 | The header carries the flags. A stream written with flags A is decoded correctly by a process with flags B. | F3 |
| TC-FMT-3 | U | NFR-COMPAT-1 | A stream with a wrong magic or an unknown version is rejected with an error. | F3 |
| TC-FMT-4 | U | FR-DEC-1 | A truncated stream (cut at every byte offset of a sample entry) returns an error and never panics. | F3 |
| TC-FMT-5 | U | FR-DEC-1 | An unknown hash returns an error. | now |

### 3.4 Offline decoding

| ID | Level | Req | Case | Task |
|---|---|---|---|---|
| TC-DEC-1 | E | FR-DEC-2, FR-DEC-3 | A sample program logs 20 distinct calls to a file. `GetIndexTable(exe)` finds all 20. | F4 |
| TC-DEC-2 | E | FR-DEC-4 | The offline hash of each call equals the hash in the stream written by the program. | F4 |
| TC-DEC-3 | E | FR-DEC-5 | Two calls with the same text and different types (`uint32(10)`, `uint64(10)`) are reported as a conflict. Two with the same text and the same types are one entry. | F4 |
| TC-DEC-4 | E | FR-DEC-6 | Calls are reported with the right file and line. | F4 |
| TC-DEC-5 | E | FR-DEC-7 | The logger imported under an alias, and held in a struct field, is found. A different package with a `Log` method is ignored. | F4 |
| TC-DEC-6 | E | FR-DEC-8 | A non-constant format string and an `interface{}` argument are reported with file and line. | F4 |
| TC-DEC-7 | E | FR-DEC-3 | A constant concatenation (`"a" + "b %d"`) is resolved to its value. | F4 |
| TC-DEC-8 | E | FR-DEC-10 | `binlogdump exe stream` output equals the lines the program computed with `fmt.Sprintf`, for every flag combination. | F5 |
| TC-DEC-9 | E | FR-DEC-2 | An executable that is not Go, a missing file and a stripped binary return an error, not a panic. | F4 |

### 3.5 FIFO

| ID | Level | Req | Case | Task |
|---|---|---|---|---|
| TC-FIFO-1 | U | FR-FIFO-1 | Write and read 1, 2, 4 and 8 byte values. | F6 |
| TC-FIFO-2 | U | FR-FIFO-2 | Blocks never straddle the end of the ring. A block that does not fit leaves a skip marker, and the reader steps over it. | F6 |
| TC-FIFO-3 | U | FR-FIFO-3 | An allocated but uncommitted block is not read, and blocks committed later are not read ahead of it. | F6 |
| TC-FIFO-4 | U | FR-FIFO-4 | Filling the ring fails the next write and increments the drop counter. Unread data is intact. | F6 |
| TC-FIFO-5 | U | FR-FIFO-6 | `Len()` is correct for empty, full, and after many wraparounds, checked against a model (a plain slice queue) with random operations. | F6 |
| TC-FIFO-6 | U | FR-FIFO-1 | A randomised test: 1e6 operations against the model, with a fixed seed. | F6 |
| TC-FIFO-7 | U | FR-CONC-2 | One producer and one consumer goroutine, 1e6 entries, under `-race`: no loss, no reorder. | F6 |
| TC-FIFO-8 | U | FR-FIFO-5 | Several producers, one consumer, under `-race`: every entry arrives intact. | F7 |
| TC-FIFO-9 | E | FR-CONC-2 | Logger with a FIFO writer: the stream read back decodes to the logged entries. | F6 |

### 3.6 Non-functional

| ID | Level | Req | Case | Task |
|---|---|---|---|---|
| TC-PERF-1 | B | NFR-PERF-1 | `BenchmarkEmptyString` within 30% of the baseline in `docs/BENCH.md`. | each task |
| TC-PERF-2 | B | NFR-PERF-2 | `BenchmarkSingleInt`, `Benchmark2Ints`, `Benchmark3Ints` within 30% of baseline. | each task |
| TC-PERF-3 | U | NFR-PERF-3 | `testing.AllocsPerRun` reports 0 for an L1 hit with zero, one and three integer arguments. | F2 |
| TC-PERF-4 | B | NFR-PERF-4 | FIFO write against `bytes.Buffer` write, same payload. | F6 |
| TC-SAFE-1 | U | NFR-SAFE-1 | `go test -race ./...` is clean. | F6 |
| TC-QUAL-1 | U | NFR-QUAL-1 | `go test -cover ./...` reports at least 80% per package. | F6 |

### 3.7 Later features (could-have)

Cases are written when the feature is scheduled. Placeholders:

- FR-SORT-1: merge of three streams with out-of-order timestamps inside and outside the window.
- FR-COMP-1: round trip for each algorithm, corrupt block returns an error.
- FR-INT-1: NDJSON output parses as JSON, one object per line, fields match the entry.

## 4. Regression suite (task 4)

The e2e suite lives in `e2e/` and contains:

- `e2e/prog/`: a small program that logs a fixed list of calls, one per supported type, and
  writes the binary stream and the expected text (`fmt.Sprintf` of the same call) to files.
- `e2e/e2e_test.go`: builds the program (default flags and `-N -l`), runs it, then decodes
  the stream with the online index table and, once built, the offline one, and compares.
- `e2e/testdata/golden_v1.bin` (added with the stream header in F3).

Each case in section 3 marked E gets a test function named after its ID, for example
`TestTC_DEC_1`. The cases that need an unbuilt feature skip with the requirement ID.
Run with `go test ./e2e/...`. Expected outcome after task 4: every case passes or skips with
a reason, and none fails.

### 4.1 State after task 4

`go test ./e2e/...` runs the sample program in 2 build modes x 16 flag combinations x 26 cases.
Known failures are listed in `knownBroken` in `e2e/e2e_test.go`, each with its requirement.
The list is empty since D10 to D13 were fixed. TC-DEC-1 to 9 and TC-FIFO-9 are implemented (F4, F5, F6 done). TC-FIFO-8 waits for F7.

## 5. Task map

| Task | Content | Cases that go green |
|---|---|---|
| F1 | `go.mod`, build scripts (done early: the e2e suite needs it) | TC-BLD-* |
| F2 | Correctness fixes (D1, D10 to D13, D15), strict verbs | The `knownBroken` e2e cases, TC-LOG-4, 8, 9, 10, TC-PERF-3 |
| F3 | Float, char, stream header | TC-LOG-6, 7, TC-FMT-1 to 4 |
| F4 | Offline index table (set `offlineDecoderReady` in `e2e/e2e_test.go`) | TC-DEC-1 to 7, 9 |
| F5 | `binlogdump` | TC-DEC-8 |
| F6 | FIFO phase 1 | TC-FIFO-1 to 7, 9, TC-PERF-4, TC-SAFE-1, TC-QUAL-1 |
| F7 | FIFO phase 2 | TC-FIFO-8 |

## 6. Exit criteria

The project meets this plan when every must-have requirement in `docs/REQUIREMENTS.md` has a
passing test, no test is skipped without a requirement ID, `go test -race ./...` is clean, and
the benchmarks are within tolerance of the recorded baseline.
