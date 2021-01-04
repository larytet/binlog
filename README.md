*So you wrote a logging library<p>
That don't impress me much*

# About

binlog is a binary log. This is likely the fastest possible implementation of a log in Golang or close to it. 
The original idea is https://github.com/ScottMansfield/nanolog/issues/4

binlog will allow you to keep logging in cases when the peformance of the official log package is too low and has significant impact on the service latency. Produced binary logs are small, encoding is faster. You pay only for variadic data you send. 
The output of a string without variadic arguments (logger adds hash of the string to the binary stream) will set you back by 15 nanoseconds. Every additional argument of the integral type costs ~10ns if this is a constant, 20ns if the argument is not a constant. 
binlog.Log() is 3x faster than fmt.Fprintf() API for an empty string and 30x faster for four arguments. A typical call to *binlog.Log()* is faster than call to *map[string]string* by order of magnitude. The binlog is fast enough to be used in the context of Go routines in a busy HTTP server.

The benchmark results below are generated by the binlog_test.go. The bottom line is that binlog is faster than anything else 
out there in the Go ecosystem by factor of *two* at least. Binlog is significantly faster than popular 
loggers like ZAP and logrus. The performance of the API is on par ("just" 2-3x slower) with C++ binary logs like 
https://github.com/PlatformLab/NanoLog

```
$ ./testall.sh 
goos: linux
goarch: amd64
pkg: binlog
BenchmarkEmptyString-4        	100000000	        14.2 ns/op
BenchmarkSingleInt-4          	50000000	        26.1 ns/op
Benchmark2Ints-4              	30000000	        37.0 ns/op
Benchmark3Ints-4              	30000000	        49.4 ns/op
PASS
coverage: 86.2%  of statements
ok  	binlog	5.324s
```
		

**Warning!** This code pushes Go quite to its limit. You will see unsafe pointers galore, ATS walk, StringHeader, explicitly forbidden, not-best-practice, makes-me-sick anti-patterns. If you prefer to trade performance for the typical code conventions compliance try https://github.com/ScottMansfield/nanolog 

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

When an application calls binlog.Log() the Log() checks the cache using the offset of the format string in the .text 
section of the executable as an index. You guessed it right - this step is insanely fast.  
 
If there is a cache miss the Log() collects all required data, adds the format string to the cache ("level 1 cache"). 
If there is a cache hit the Log() outputs hash of the format string and all variadic parameters to the specified io.Writer
If the string is not from the .text section (allocated from a heap, for example) the Log() stores the string in the map ("level 2 cache").

The cache (L1 and L2) contains the information required for decoding and formatting of the binary data. Things like size 
of the argument, format "verb", number of arguments, hash of the format string, the format string are all in the cache. 


# Install

You need something like ```../../bin/dep ensure --update``` or something like 
```go get "github.com/larytet-go/procfs" "github.com/larytet-go/sprintf"  "github.com/larytet-go/moduledata"``` to install missing packages

# Limitations

I did not test for Windows. 

The code relies on the facts that:
 
*  the strings in Go are located in the same ELF file segment.
*  ELF has a unique address for every string in the source code.

Deduplication of the strings is a real possibility in the future. Deduplication is a trivial thing to implement given the Go AST. If Go starts to dedup the strings I 
will need a larger key in the cache than just an address of the string. This will seriously impact the performance. If you care about fast logging
make sure to vote/post comment here https://github.com/golang/go/issues/28864. May be one day "log" package will cache the strings and support a binary output
as well. 


The API is not thread safe. One possible workaround is to have an instance of the binlog in every thread. 
The application is expected to flush the output to a file/stdout from time to time.
An application can share the io.Writer object between the binary logs if the application implements WriterControl.
Add index and/or a timestamp (see SEND_LOG_INDEX) to all log entries, order the log entries when printing for human consumption. Atomic counter will set you back 
by 25ns per call (Go sync/atomic is not very fast).

This logger will not work well for applications which allocate format strings dynamically, like in the code below. The performance will be similar to 
ZAP log & some of its faster friends.  

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

More benchmark for different logging frameworks (Spoiler: NOP loggers which do nothing require 100ns/op)

* https://gist.github.com/Avinash-Bhat/48c4f06b0cc840d9fd6c
* https://stackoverflow.com/questions/10571182/go-disable-a-log-logger
* https://blapid.github.io/cpp/2017/10/31/llcpp-a-quest-for-faster-logging-part-2.html
* https://github.com/gabime/spdlog/issues/942
* https://github.com/zuhd-org/easyloggingpp
* https://godoc.org/github.com/alexcesaro/log/stdlog
* https://github.com/hashicorp/logutils
* https://github.com/romana/rlog

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
