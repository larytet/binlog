
This is likely the fastest possible implementation of a log in Golang or close to it


	$ ./testall.sh 
	goos: linux
	goarch: amd64
	pkg: binlog
	BenchmarkFifo-4   	30000000	        40.2 ns/op
	PASS
	coverage: 74.5% of statements
	ok  	binlog	1.428s
	
	
Linux only. Relies on the fact the strings in Go are located in the same ELF file segment. 
The performance of the API is on par ("just" 3-4x slower) with C++ binary logs like https://github.com/PlatformLab/NanoLog
For example, a call to a method returning two values costs ~2ns in Golang. Golang does not inline functions often. 
The original idea is https://github.com/ScottMansfield/nanolog/issues/4

Example:

```Go
{
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	binlog.Log("Hello %u", 10)
}
```