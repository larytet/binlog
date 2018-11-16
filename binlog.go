package binlog

import (
	"log"
	"path/filepath"
	"reflect"
	"unsafe"
)

var baseString string = ""
var baseStringAddress uintptr = (*reflect.StringHeader)(unsafe.Pointer(&baseString)).Data

func Init() {
	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		log.Fatal(err)
	}
}

func PrintUint32(s string, args ...uint32) {

}
