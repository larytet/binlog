
This is likely the fastest possible implementation of a log in Golang or close to it


	goos: linux
	goarch: amd64
	pkg: binlog
	Benc	hmarkFifo-4   	200000000	         8.66 ns/op
	PASS
	coverage: 40.6% of statements
	ok  	binlog	2.807s
	
	
Linux only. Relies on the fact the strings in Go are located in the same ELF file segment. 
