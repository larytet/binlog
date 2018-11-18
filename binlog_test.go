package binlog

import (
	"github.com/jandre/procfs"
	"os"
	"reflect"
	"testing"
	"unsafe"
)

func align(v uintptr) uintptr {
	mul := uintptr((1 << 3) - 1)
	return (v + mul) & (^uintptr(mul))
}

func getStringSize(s string) uintptr {
	return align(uintptr(len(s)))
}

func TestStringLocation(t *testing.T) {
	s0 := "Hello, world"
	s1 := "Hello, world2"
	p0 := uintptr(unsafe.Pointer(&s0))
	p1 := uintptr(unsafe.Pointer(&s1))

	p2 := p0 + getStringSize(s0)
	p3 := p1 + getStringSize(s1)
	if p1 != p2 && p0 != p3 {
		t.Fatalf("Bad locations %x %x, expected %x %x", p0, p1, p2, p3)
	}
}

var s0 string = "Hello, world"
var s1 string = "Hello, world2"

func TestStringLocationGlobalLocalHeader(t *testing.T) {
	s1 := "Hello, world2"
	hdr0 := (*reflect.StringHeader)(unsafe.Pointer(&s0))
	hdr1 := (*reflect.StringHeader)(unsafe.Pointer(&s1))
	if hdr0.Data+0x100 > hdr1.Data {
		t.Fatalf("Bad locations %x %x", hdr0.Data, hdr1.Data)
	}
}

func TestStringLocationGlobal(t *testing.T) {
	p0 := uintptr(unsafe.Pointer(&s0))
	p1 := uintptr(unsafe.Pointer(&s1))

	p2 := p0 + getStringSize(s0)
	p3 := p1 + getStringSize(s1)
	if p1 != p2 && p0 != p3 {
		t.Fatalf("Bad locations %x %x, expected %x %x", p0, p1, p2, p3)
	}
}

func TestStringLocationGlobalLocal(t *testing.T) {
	s1 := "Hello, world2"
	p0 := uintptr(unsafe.Pointer(&s0))
	p1 := uintptr(unsafe.Pointer(&s1))
	if p0 != p1 {
		//t.Fatalf("Bad locations %x %x", p0, p1)
	}
}

type Statm struct {
	Size     int64 // total program size (pages)(same as VmSize in status)
	Resident int64 //size of memory portions (pages)(same as VmRSS in status)
	Shared   int   // number of pages that are shared(i.e. backed by a file)
	Trs      uint  // number of pages that are 'code'(not including libs; broken, includes data segment)
	Lrs      int   //number of pages of library(always 0 on 2.6)
	Drs      int   //number of pages of data/stack(including libs; broken, includes library text)
	Dt       int   //number of dirty pages(always 0 on 2.6)
}

func TestInit(t *testing.T) {
	selfPid := os.Getpid()
	process, err := procfs.NewProcess(selfPid, true)
	if err != nil {
		t.Fatalf("Fail to read procfs context %v", err)
	}
	maps, err := process.Maps()
	statm, err := process.Statm()
	constDataBase := statm.Trs
	constDataSize := maps[0].AddressStart
	binlog := Init(uintptr(constDataBase), uint(constDataSize))
	binlog.PrintUint32("PrintUint32 %u", 10)
}
