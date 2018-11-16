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
