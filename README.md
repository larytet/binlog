# About

This is likely the fastest possible implementation of a log in Golang or close to it
This logger will allow you to keep logging in many cases when the peformance of the official log package is too low.
The binary log is small and fast. You pay only for variadic data you send. 
Output of a string without variadic arguments (logger adds hash of the string to the binary stream) will set you back by 15ns.
Every additional argument of the integral type costs ~10ns if this is a constant. 
binlog.Log() is 3x faster than fmt.Fprintf() API

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
	
	
The performance of the API is on par ("just" 2-3x slower) with C++ binary logs like https://github.com/PlatformLab/NanoLog
The original idea is https://github.com/ScottMansfield/nanolog/issues/4

**Warning!** This code pushes Go quite to it's limit. There are unsafe pointers, ATS walk, StringHeader and
other explicitly-forbidden/anti-pattern/not-best-practice/makes-me-sick things galore for the taste of many.
If you want trade performance for compliance try https://github.com/ScottMansfield/nanolog 

Example:

```Go
package main

import (
	"bytes"
	"github.com/larytet/binlog"
)

func main() {
	var buf bytes.Buffer
	constDataBase, constDataSize := binlog.GetSelfTextAddressSize()
	binlog := binlog.Init(&buf, constDataBase, constDataSize)
	binlog.Log("Hello %u", 10)
}
```

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
make sure to vote/post comment here https://github.com/golang/go/issues/28864. May be one day "log" package wiill cache the strings and support a binary outuptu 
as well. 


The API is not thread safe. One prossible workaround is to have an instance of the binlog in every thread, and flush the output to a file/stdout from time to time.
Add index and/or a timestamp (see SEND_LOG_INDEX) to all log entries, order the log entries when printing for human consumption. Atomic counter will set you back 
by 25ns per call (Go sync/atomic is not very fast).

This logger will not work well for applications which allocate format strings dynamically, like in the code below. The performance will be similar to 
ZAP log & some of it's faster friends.  

```Go
{
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	err := binlog.Log(fmtString, value) // relatively slow "L2 cache" is used here
}
```

The following popular formats are not supported: "%v", "%T", "%c", "%p"



# Links

More benchmark for different logging frameworks (Spoiler: NOP loggers which do nothing require 100ns/op)

* https://gist.github.com/Avinash-Bhat/48c4f06b0cc840d9fd6c
* https://stackoverflow.com/questions/10571182/go-disable-a-log-logger

Golang related stuff 

* https://github.com/golang/go/issues/28864
* https://medium.com/justforfunc/understanding-go-programs-with-go-parser-c4e88a6edb87
* https://stackoverflow.com/questions/50524607/go-lang-func-parameter-type
* https://stackoverflow.com/questions/46115312/use-ast-to-get-all-function-calls-in-a-function
* https://github.com/uber-go/zap/issues/653
* https://groups.google.com/forum/#!topic/golang-nuts/Og8s9Y-Kif4


# Todo

Add hash of the strings to the binary stream. Parse the Go sources, collect and hash all strings in calls to the binlog. Decode binary streams
using only the source files. Should I assume that calls to the log look like xx.Log("...", arg1, ...)?

Add suport for "string", "float"

Optimize "writeArgumentToOutput"