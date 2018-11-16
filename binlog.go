package binlog

import (
	"log"
	"os"
)

func Init() {
	executable, err := os.Executable()
	if err != nil {
		log.Fatal("Failed to get eecutable", err)
	}
	file, err := os.Open(executable)
	if err != nil {
		log.Fatalf("Error while opening file %s %v", executable, err)
	}
	defer file.Close()

}

func PrintUint32(s string, args ...uint32) {

}
