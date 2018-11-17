package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	//	"C"
	"log"
	//"runtime"
	//	"os"
	"unsafe"
)

type Binlog struct {
	baseOffset uintptr
}

// moduledata is a dead end?
// See https://stackoverflow.com/questions/48445593/go-function-definition-in-another-package
// https://golang.org/cmd/cgo/#hdr-Go_references_to_C
//go:noescape
//go:linkname runtime_firstmoduledata runtime.firstmoduledata
//var Runtime_firstmoduledata uintptr

// Straight from https://github.com/larytet/restartable/blob/master/restartable.go
// and https://golang.org/src/runtime/symtab.go?m=text
// See also https://stackoverflow.com/questions/37251108/golang-fetch-all-all-filepathes-from-compiled-file
// https://blog.altoros.com/golang-internals-part-6-bootstrapping-and-memory-allocator-initialization.html
// http://www.alangpierce.com/blog/2016/03/17/adventures-in-go-accessing-unexported-functions/

func Init() *Binlog {
	var firstmoduledata *moduledata = (*moduledata)(unsafe.Pointer(&runtime_firstmoduledata))
	for md := firstmoduledata; md != nil; md = md.next {
		if md.bad {
			continue
		}
		data := md.noptrdata
		log.Printf("%v", data)
	}
	var binlog Binlog

	return &binlog
}

func PrintUint32(s string, args ...uint32) {

}
