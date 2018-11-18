package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	"fmt"
	//"runtime"
	//	"os"
)

// typedef int (*intFunc) ();
//
// int
// bridge_int_func(intFunc f)
// {
//		return f();
// }
//
// int fortytwo()
// {
//	    return 42;
// }
import "C"

type Binlog struct {
	constDataBase uintptr
	constDataSize uint
}

// moduledata is a dead end?
// See https://stackoverflow.com/questions/48445593/go-function-definition-in-another-package
// https://golang.org/cmd/cgo/#hdr-Go_references_to_C
////go:noescape
////go:linkname runtime_firstmoduledata runtime.firstmoduledata
//var Runtime_firstmoduledata uintptr

// Straight from https://github.com/larytet/restartable/blob/master/restartable.go
// and https://golang.org/src/runtime/symtab.go?m=text
// See also https://stackoverflow.com/questions/37251108/golang-fetch-all-all-filepathes-from-compiled-file
// https://blog.altoros.com/golang-internals-part-6-bootstrapping-and-memory-allocator-initialization.html
// http://www.alangpierce.com/blog/2016/03/17/adventures-in-go-accessing-unexported-functions/

// constDataBase is an address of the initialzied const data, constDataSize is it's size
func Init(constDataBase uintptr, constDataSize uint) *Binlog {
	binlog := &Binlog{constDataBase: constDataBase, constDataSize: constDataSize}
	return binlog
}

func (b *Binlog) PrintUint32(s string, args ...uint32) {
	f := C.intFunc(C.fortytwo)
	fmt.Println(int(C.bridge_int_func(f)))
}
