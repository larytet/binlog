# About

This is likely the fastest possible implementation of a log in Golang or close to it
This logger will allow you to keep logging in many cases when the peformance of the official log package is too low.
The binary log is small and fast. You pay only for variadic data you send. If you output an integer you can do better than 40ns/log
Output of an empty string (logger adds hash of the string to the binary stream) will set you back by 10ns.


	$ ./testall.sh 
	goos: linux
	goarch: amd64
	pkg: binlog
	BenchmarkEmptyString-4   	100000000	        15.3 ns/op
	BenchmarkSingleInt-4     	30000000	        40.6 ns/op
	PASS
	coverage: 86.2%  of statements
	ok  	binlog	5.324s
	
	
Linux only. Relies on the fact the strings in Go are located in the same ELF file segment. 
The performance of the API is on par ("just" 3-4x slower) with C++ binary logs like https://github.com/PlatformLab/NanoLog
For example, a call to a method returning two values costs ~2ns in Golang. Golang does not inline functions often. 
The original idea is https://github.com/ScottMansfield/nanolog/issues/4

Warning! This code pushes Go quite to it's limit. There are unsafe pointers, ATS walk, StringHeader and
other forbidden things galore for any taste.

Example:

```Go
{
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, constDataBase, constDataSize)
	binlog.Log("Hello %u", 10)
}
```

# Limitations

The API is not thread safe. One prossible workaround is to have an instance of the binlog in every thread, and flush the output to a file/stdout from time to time.
Add index and/or a timestamp all log entries and order the log entries when printing for human consumption

This logger will not work well for applications which allocate format strings dynamically, like in the code below 

```Go
{
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	err := binlog.Log(fmtString, value)
}
```

# Links

More benchmark for different logging frameworks (Spoiler: NOP loggers which do nothing require 100ns/op)

* https://gist.github.com/Avinash-Bhat/48c4f06b0cc840d9fd6c
* https://stackoverflow.com/questions/10571182/go-disable-a-log-logger

Golang related stuff 

* https://github.com/golang/go/issues/28864
* https://medium.com/justforfunc/understanding-go-programs-with-go-parser-c4e88a6edb87
* https://stackoverflow.com/questions/50524607/go-lang-func-parameter-type
* https://stackoverflow.com/questions/46115312/use-ast-to-get-all-function-calls-in-a-function
* 


# Todo

Add hash of the strings to the binary stream. Parse the Go sources, collect and hash all strings in calls to the binlog. Decode binary streams
using only the source files. Should I assume that calls to the log look like xx.Log("...", arg1, ...)?

