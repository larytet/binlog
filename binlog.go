package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	//	"C"
	"log"
	"runtime"
	//	"os"
	_ "unsafe"
)

type Binlog struct {
	baseOffset uintptr
}

// See https://stackoverflow.com/questions/48445593/go-function-definition-in-another-package

//go:noescape
//go:linkname firstmoduledata runtime.firstmoduledata
var firstmoduledata runtime.moduledata

func Init() *Binlog {
	for md := &firstmoduledata; md != nil; md = md.next {
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
