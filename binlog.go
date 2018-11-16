package binlog

import (
	"log"
	"os"
)

type Binlog struct {
	baseOffset uintptr
}

// Read the executable, look for "hmst", assume that this is where all strings are
// this is my base address for all strings in the system
// See https://www.jonathan-petitcolas.com/2014/09/25/parsing-binary-files-in-go.html
func Init() *Binlog {
	executable, err := os.Executable()
	if err != nil {
		log.Fatal("Failed to get eecutable", err)
	}
	file, err := os.Open(executable)
	if err != nil {
		log.Fatalf("Error while opening file %s %v", executable, err)
	}
	defer file.Close()
	base := getOffset

	var binlog Binlog

	return &binlog
}

func PrintUint32(s string, args ...uint32) {

}
