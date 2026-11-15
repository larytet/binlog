*So you've written a logging library?*  
*That does not impress me much.*

# About

`binlog` is a binary logging library for Go. It is likely one of the fastest logging implementations in the Go ecosystem, or close to it.

The original idea is here: https://github.com/ScottMansfield/nanolog/issues/4

`binlog` is useful when the standard `log` package is too slow and adds noticeable latency to a service. Binary logs are smaller, and encoding is faster. You only pay for the dynamic data that you send.

Logging a constant string with no variadic arguments, where the logger only writes the hash of the string to the binary stream, takes about 15 ns. Each additional integer argument costs about 10 ns for a constant and about 20 ns for a non constant value.

`binlog.Log()` is about 3x faster than `fmt.Fprintf()` for an empty string and about 30x faster for a call with four arguments. A typical call to `binlog.Log()` is faster than a `map[string]string` access. `binlog` is fast enough for a busy HTTP server.

The benchmark results below come from `binlog_test.go` on an older machine (see `docs/BENCH.md` for a recent baseline). In practice, `binlog` is at least 2x faster than other Go logging libraries that I have tested. It is also much faster than popular loggers such as Zap and Logrus. Its API performance is reasonably close to C++ binary loggers such as NanoLog.

```text
$ ./testall.sh
goos: linux
goarch: amd64
pkg: binlog
BenchmarkEmptyString-4         100000000        14.2 ns/op
BenchmarkSingleInt-4            50000000        26.1 ns/op
Benchmark2Ints-4                30000000        37.0 ns/op
Benchmark3Ints-4                30000000        49.4 ns/op
PASS
coverage: 86.2% of statements
ok      binlog  5.324s
```
		

**Warning!** This code pushes Go close to its limits. It uses unsafe pointers, AST walking, StringHeader, and several techniques that are explicitly discouraged in normal Go code.
If you prefer a more conventional design and are willing to trade performance for cleaner code, see: https://github.com/ScottMansfield/nanolog

# Usage

```Go
package main

import (
	"bytes"

	"github.com/larytet/binlog"
)

func main() {
	var buf bytes.Buffer
	base, size := binlog.GetSelfTextAddressSize()
	logger := binlog.Init(&buf, &binlog.WriterControlDummy{}, base, size)
	logger.WriteHeader() // optional: the stream then describes itself
	logger.Log("Hello %d %s %g", 10, "world", 2.5)
}
```

Supported verbs: `%d %i %x %c` for integers (8 to 64 bits, signed or unsigned, and runes), `%f %e %g` for
`float32` and `float64`, `%s` for strings of any length, and `%%`. Other verbs and verbs that do not fit the
argument type make `Log()` return an error and write nothing.

## Decoding a log offline

`binlogdump` prints a binary log as text. It needs the executable that wrote the log, and the sources of that
executable on the same machine (the format strings and argument types are read from them with `go/packages`):

```text
go install github.com/larytet/binlog/cmd/binlogdump@latest
binlogdump ./myservice service.binlog          # stream with a header
binlogdump -flags 8 ./myservice service.binlog # stream without a header: 8 = timestamps
```

`-format ndjson` prints one JSON object per entry, and `-format es-bulk -index NAME` adds the Elasticsearch bulk
action line, so that the output can be sent to `_bulk` as it is.

From Go: `ast.GetIndexTable(executable)` returns the table for `binlog.DecodeNextFlags`.
`ast.LoadTable` also returns the calls that could not be analysed (a format string that is not a constant,
arguments passed as `args...`, an `interface{}` argument) and the calls that cannot share a hash (the same
format text with different argument types).

## Buffering: Fifo

`binlog/io` has a lock free ring buffer, `Fifo`, for the thread which writes the log to a file or a socket.
Several producers and one consumer are supported. `io.NewFramer(fifo)` is both the writer and the
`WriterControl` of a logger and makes every log entry one block, so that a consumer never sees half an entry.
Several loggers, each with its own `Framer`, can share one `Fifo`:

```Go
fifo := io.New(1 << 20)
framer := io.NewFramer(fifo)
logger := binlog.Init(framer, framer, base, size)
// consumer goroutine: fifo.Pop(buf), or binlog.DecodeNextFlags(fifo, ...) since Fifo is an io.Reader
```

If the ring is full the entry is dropped and counted (`Dropped()`).

# How it works

When an application calls `binlog.Log()`, the logger checks a cache using the offset of the format string in the executable as an index. This step is fast, like 2 opcodes fast.
On a cache miss, `Log()` collects the required metadata and stores the format string in the cache, called the L1 cache.
On a cache hit, `Log()` writes the hash of the format string and all variadic arguments to the target io.Writer.
If the string does not come from the executable image, for example if it was allocated on the heap, `Log()` stores it in a map, called the L2 cache.
The L1 and L2 caches store the data required to decode and format the binary stream later. This includes argument sizes, format verbs, number of arguments, the hash of the format string, and the format string itself.

# Install

`go get github.com/larytet/binlog` (Go 1.27 or later). The library has no dependencies outside the standard library; `binlogdump` uses `golang.org/x/tools/go/packages`.

`go test ./...` runs the unit tests and the end to end tests (about a minute: they build programs and load their sources). `docs/` has the requirements, the design, the test plan and the benchmark baseline.

# Limitations

Linux and amd64 are tested. The code reads `/proc/self/maps`, and `binlogdump` reads ELF executables. Windows and macOS are not supported.

The code relies on these assumptions:
 
*  Go string literals are located in the same ELF segment.
*  ELF provides a unique address for each string literal in the source code.

String deduplication may become a real issue in the future. If Go starts deduplicating strings, the cache key will need to be larger than a single string address. That will hurt performance.

If fast logging matters to you, please comment or vote here: https://github.com/golang/go/issues/28864

Maybe one day the standard `log` package will cache format strings and support binary output as well.

A `Binlog` instance is not thread safe: use one instance per goroutine. Instances can share one output through a `Fifo` (see above).

The application is expected to flush output to a file or to stdout from time to time.

An application can share an `io.Writer` between multiple binary loggers if it implements `WriterControl`.

You can also add an index or timestamp to all log entries, sort them later, and print them in human readable order. An atomic counter costs about 25 ns per call, since Go `sync/atomic` is not especially fast.

This logger will not work well for applications that build format strings dynamically, as in the example below. In that case, performance will be closer to Zap and similar loggers.

```Go
{
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	err := binlog.Log(fmtString, value) // relatively slow "L2 cache" is used here
}
```

The following formats are not supported: "%v", "%T", "%p".


Offline decoding needs the executable and its sources, not the sources alone. A stripped executable is fine, a Go executable built with `-trimpath` is not (the file table has no absolute paths).



# Links

Presentation: https://docs.google.com/presentation/d/1WuY5eifDb0XcCtYMhoj6CdqNL2_AVCRPVpbvmZUwtBg

More benchmark for different logging frameworks (Spoiler: doing nothing NOP loggers require 100ns/op)

* https://gist.github.com/Avinash-Bhat/48c4f06b0cc840d9fd6c
* https://stackoverflow.com/questions/10571182/go-disable-a-log-logger
* https://blapid.github.io/cpp/2017/10/31/llcpp-a-quest-for-faster-logging-part-2.html
* https://github.com/gabime/spdlog/issues/942
* https://github.com/zuhd-org/easyloggingpp
* https://godoc.org/github.com/alexcesaro/log/stdlog
* https://github.com/hashicorp/logutils
* https://github.com/romana/rlog
* https://github.com/morganstanley/binlog   C++ binlog

Golang related stuff 

* https://github.com/golang/go/issues/28864
* https://medium.com/justforfunc/understanding-go-programs-with-go-parser-c4e88a6edb87
* https://stackoverflow.com/questions/50524607/go-lang-func-parameter-type
* https://stackoverflow.com/questions/46115312/use-ast-to-get-all-function-calls-in-a-function
* https://github.com/uber-go/zap/issues/653
* https://groups.google.com/forum/#!topic/golang-nuts/Og8s9Y-Kif4
* https://github.com/elazarl/gosloppy - probably contains more example of Go parsing


# Todo

Done since the first version: offline decoding, a stream header, floats and runes, signed integers, empty and
long strings, the FIFO (several producers), `binlogdump`. See `docs/REQUIREMENTS.md` for the status of every
requirement.

Open:

* Run-time sorting of the entries of several streams, and compression of the stream.
* Integration with Megalog (its interface is not defined yet).
* The speed on the benchmark machine is 3 to 4 times slower than the numbers at the top (older, faster
  hardware). `docs/BENCH.md` has the current baseline. The FIFO write path is about 40 ns slower than
  a `bytes.Buffer` write.
* The runtime logger does not detect two calls with the same format text and different argument types
  (the offline table reports them as a conflict).
