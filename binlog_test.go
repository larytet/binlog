package binlog

import (
	"testing"
	"unsafe"
)

func BenchmarkCheckStringPointer(b *testing.B) {
	s := "Hello, world"
	p0 := uintptr(unsafe.Pointer(&s))
	for i := 0; i < b.N; i++ {
		p := uintptr(unsafe.Pointer(&s))
		if p != p0 {
			b.Fatalf("Different pointers %v %v", p0, p)
		}
	}
}

func align(v uintptr) uintptr {
	return (v + 0x03) & (^uintptr(0x03))
}

func TestStringLocation(t *testing.T) {
	s0 := "Hello, world"
	s1 := "Hello, world2"
	p0 := uintptr(unsafe.Pointer(&s0))
	p1 := uintptr(unsafe.Pointer(&s1))

	p2 := p0 + align(uintptr(len(s0)))
	p3 := p1 + align(uintptr(len(s1)))
	if p1 != p2 && p0 != p3 {
		t.Fatalf("Unorered locations %x %x, expected %x %x", p0, p1, p2, p3)
	}
}
