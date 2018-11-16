package binlog

import (
	"log"
	"reflect"
	"unsafe"
)

var baseString string = ""
var baseStringAddress uintptr = (*reflect.StringHeader)(unsafe.Pointer(&baseString)).Data

func PrintUint32(s string, args ...uint32) {
	log.Printf("%v", unsafe.Pointer(&s))
}
