*So you've written a logging library?*  
*That does not impress me much.*

# About

`binlog` is a binary logging library for Go. It is likely one of the fastest logging implementations in the Go ecosystem, or close to it.

The original idea is here: https://github.com/ScottMansfield/nanolog/issues/4

`binlog` is useful when the standard `log` package is too slow and adds noticeable latency to a service. Binary logs are smaller, and encoding is faster. You only pay for the dynamic data that you send.

Logging a constant string with no variadic arguments, where the logger only writes the hash of the string to the binary stream, takes about 15 ns. Each additional integer argument costs about 10 ns for a constant and about 20 ns for a non constant value.

`binlog.Log()` is about 3x faster than `fmt.Fprintf()` for an empty string and about 30x faster for a call with four arguments. A typical call to `binlog.Log()` is faster than a `map[string]string` access. `binlog` is fast enough for a busy HTTP server.

The benchmark results below come from `binlog_test.go`. In practice, `binlog` is at least 2x faster than other Go logging libraries that I have tested. It is also much faster than popular loggers such as Zap and Logrus. Its API performance is reasonably close to C++ binary loggers such as NanoLog.

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
	constDataBase, constDataSize := binlog.GetSelfTextAddressSize()
	binlog := binlog.Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
	binlog.Log("Hello %u", 10)
}
```

# How it works

When an application calls `binlog.Log()`, the logger checks a cache using the offset of the format string in the executable as an index. This step is fast, like 2 opcodes fast.
On a cache miss, `Log()` collects the required metadata and stores the format string in the cache, called the L1 cache.
On a cache hit, `Log()` writes the hash of the format string and all variadic arguments to the target io.Writer.
If the string does not come from the executable image, for example if it was allocated on the heap, `Log()` stores it in a map, called the L2 cache.
The L1 and L2 caches store the data required to decode and format the binary stream later. This includes argument sizes, format verbs, number of arguments, the hash of the format string, and the format string itself.

# Install

You need something like ```../../bin/dep ensure --update``` or something like 
```go get "github.com/larytet-go/procfs" "github.com/larytet-go/sprintf"  "github.com/larytet-go/moduledata"``` to install missing packages

After all packages are installed this should work ```go test .```

# Limitations

I did not test for Windows. 

The code relies on these assumptions:
 
*  Go string literals are located in the same ELF segment.
*  ELF provides a unique address for each string literal in the source code.

String deduplication may become a real issue in the future. If Go starts deduplicating strings, the cache key will need to be larger than a single string address. That will hurt performance.

If fast logging matters to you, please comment or vote here: https://github.com/golang/go/issues/28864

Maybe one day the standard `log` package will cache format strings and support binary output as well.

The API is not thread safe. One possible workaround is to keep one binlog instance per thread.

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

The following popular formats are not supported: "%v", "%T", "%c", "%p"


Offline decoding using only source files is a work in progress. 



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

Decode binary streams using only the source files or the executable. Allow offline decode of the binary streams. Parse the Go sources or executable 
collect and hash all strings in calls to the binlog. Should I assume that calls to the log look like xx.Log("...", arg1, ...)?
What happens if there are two calls like this
 
```Go
bin.Log("Number %d", uint32(10))
bin.Log("Number %d", uint64(10)) 
```
I need unique identification: (hash of) filename and line in the code
 
Parse ELF route. Try ```readelf --hex-dump=.rodata  ELF-FILENAME```

Add suport for "float", "char"

Output hash of the constant strings instead of strings themselves. More AST stuff here can allow to skip the constants.

Add a "writer" based on FIFO. The idea is to "allocate" the necessary number of bytes from the FIFO starting from the tail, mark the start of the block as "allocated", return a pointer to the 
allocated block. The application copies the data to the block, marks the block as "initialized". The "consumer" (a thread which dumps the logs) reverses the process.
When allocating blocks the FIFO always allocates continuous memory areas. If there is not enough place between the tail and the end of the FIFO the allocator marks 
the unused area as skipped and attempts to allocate a block from offset zero.

**Add run-time sorting and compression of the logs**. Use a parameter for the window size. 

**Integration with Megalog and ElasticSearch**
