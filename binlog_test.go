package binlog

import (
	"github.com/jandre/procfs"
	"github.com/jandre/procfs/maps"
	"github.com/jandre/procfs/statm"
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

func getTextAddressSize(s *statm.Statm, m []*maps.Maps) (constDataBase uint, constDataSize uint) {
	constDataBase = uint(s.Trs)
	constDataEnd := m[0].AddressEnd
	moduleName := m[0].Pathname

	for i := 1; i < len(m); i++ {
		if moduleName != m[i].Pathname {
			break
		}
		constDataEnd = m[i].AddressEnd
	}

	constDataSize = uint(constDataEnd) - constDataBase
	return constDataBase, constDataSize
}

func TestInit(t *testing.T) {
	selfPid := os.Getpid()
	process, err := procfs.NewProcess(selfPid, true)
	if err != nil {
		t.Fatalf("Fail to read procfs context %v", err)
	}
	maps, err := process.Maps()
	if err != nil {
		t.Fatalf("Fail to read procfs/maps context %v", err)
	}
	statm, err := process.Statm()
	if err != nil {
		t.Fatalf("Fail to read procfs/statm context %v", err)
	}
	constDataBase, constDataSize := getTextAddressSize(statm, maps)
	binlog := Init(uint(constDataBase), uint(constDataSize))
	binlog.PrintUint32("PrintUint32 %u", 10)
}
