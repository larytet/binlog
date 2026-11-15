# Benchmark baseline

Recorded on 2026-11-15 with Go 1.27.4, linux/amd64, 12 hardware threads (a virtualised
machine, not the machine of the README numbers). Run:

```
go test -run XXX -bench 'BenchmarkEmptyString$|Benchmark2Ints$|Benchmark3Ints$' .
go test -run XXX -bench 'Fifo|BytesBuffer' ./io
```

TC-PERF-1 and TC-PERF-2 compare a later run against these values with a 30% tolerance.

| Benchmark | ns/op | Requirement |
|---|---|---|
| `BenchmarkEmptyString` (constant string, no arguments) | 51 | NFR-PERF-1: README says 14, target "about 15, at most 25" |
| `BenchmarkBinLogEmptyString` | 48 | |
| `Benchmark2Ints` | 82 | NFR-PERF-2 |
| `Benchmark3Ints` | 119 | NFR-PERF-2: the README says 49 |
| `BenchmarkFifoWriteIntegral` (8 bytes) | 62 to 67 | NFR-PERF-4 |
| `BenchmarkFifoAllocCommit` (8 bytes) | 59 to 61 | NFR-PERF-4 |
| `BenchmarkBytesBufferWrite8` | 22 to 24 | reference |

## Notes

- NFR-PERF-1 and NFR-PERF-2 are **not met on this machine**: the numbers are 3 to 4 times the README's.
  The README numbers came from an older, faster machine and an older Go. The baseline above is the
  reference for regressions; whether the README target holds needs a run on comparable hardware.
  The code path was not slowed by this project's changes: the hit path gained one pointer comparison (D16).
- NFR-PERF-4 is **not met**: a FIFO write costs about 40 ns more than a `bytes.Buffer` write, against a
  target of 10 ns. Each write does two atomic stores (the commit and the tail), and an atomic store on
  amd64 is an `XCHG`. The first design had three and the first measurement was 94 ns. Going lower needs
  a design without a published tail, for example a sequence number in each block header, which is a
  larger change for a "should" requirement.
- NFR-PERF-3 (zero allocations on an L1 hit) is checked by `TestAllocsPerLog`.
