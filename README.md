
This is likely the fastest possible implementation of a log in Golang or close to it


	goos: linux
	goarch: amd64
	pkg: binlog
	Benc	hmarkFifo-4   	200000000	         8.66 ns/op
	PASS
	coverage: 40.6% of statements
	ok  	binlog	2.807s
	
	
Linux only. Relies on the fact the strings in Go are located in the same ELF file segment. 
The performance of the API is on par with C++ binary logs like https://github.com/PlatformLab/NanoLog 
The original idea is https://github.com/ScottMansfield/nanolog/issues/4

Example:

```Go

func getTextAddressSize(maps []*maps.Maps) (constDataBase uint, constDataSize uint) {
	s := "TestString"
	sAddress := uint(getStringAdress(s))
	for i := 0; i < len(maps); i++ {
		start := uint(maps[i].AddressStart)
		end := uint(maps[i].AddressEnd)
		if (sAddress >= start) && (sAddress <= end) {
			return start, end - start
		}
	}

	return 0, 0
}

func getSelfTextAddressSize() (constDataBase uint, constDataSize uint) {
	selfPid := os.Getpid()
	process, err := procfs.NewProcess(selfPid, true)
	if err != nil {
		log.Fatalf("Fail to read procfs context %v", err)
	}
	maps, err := process.Maps()
	if err != nil {
		log.Fatalf("Fail to read procfs/maps context %v", err)
	}
	return getTextAddressSize(maps)

}

func TestInit(t *testing.T) {
	constDataBase, constDataSize := getSelfTextAddressSize()
	binlog := Init(uint(constDataBase), uint(constDataSize))
	binlog.PrintUint32("PrintUint32 %u", 10)
}
```