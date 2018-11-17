package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	//	"C"
	"log"
	//"runtime"
	//	"os"
	_ "unsafe"
)

type Binlog struct {
	baseOffset uintptr
}

// See https://stackoverflow.com/questions/48445593/go-function-definition-in-another-package
// https://golang.org/cmd/cgo/#hdr-Go_references_to_C

//go:noescape
//go:linkname runtime_firstmoduledata runtime.firstmoduledata
var runtime_firstmoduledata uintptr

// Straight from https://golang.org/src/runtime/symtab.go?m=text
type functab struct {
	entry   uintptr
	funcoff uintptr
}

type textsect struct {
	vaddr    uintptr // prelinked section vaddr
	length   uintptr // section length
	baseaddr uintptr // relocated section address
}

type moduledata struct {
	pclntable             []byte
	ftab                  []functab
	filetab               []uint32
	findfunctab           uintptr
	minpc, maxpc          uintptr
	text, etext           uintptr
	noptrdata, enoptrdata uintptr
	data, edata           uintptr
	bss, ebss             uintptr
	noptrbss, enoptrbss   uintptr
	end, gcdata, gcbss    uintptr
	types, etypes         uintptr
	textsectmap           []textsect
	typelinks             []int32 // offsets from types
	itablinks             []*itab

	ptab []ptabEntry

	pluginpath string
	pkghashes  []modulehash

	modulename   string
	modulehashes []modulehash

	hasmain uint8 // 1 if module contains the main function, 0 otherwise

	gcdatamask, gcbssmask bitvector

	typemap map[typeOff]*_type // offset to *_rtype in previous module

	bad bool // module failed to load and should be ignored

	next *moduledata
}

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
