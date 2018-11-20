package binlog

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
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

func TestReadme(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	binlog.Log("Hello %d", 10)
}

func TestInt(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	fmtString := "Hello %d"
	rand.Seed(42)
	value0 := rand.Int31()
	binlog.Log(fmtString, value0)
	var hash uint32
	err := binary.Read(&buf, binary.LittleEndian, &hash)
	if err != nil {
		t.Fatalf("Failed to read back hash %v", err)
	}
	var index uint32
	err = binary.Read(&buf, binary.LittleEndian, &index)
	if err != nil {
		t.Fatalf("Failed to read back index %v", err)
	}
	if index != 1 {
		t.Fatalf("Index is %d instead of 1", index)
	}
	var value1 int32
	err = binary.Read(&buf, binary.LittleEndian, &value1) // bytes.NewBuffer(bufBytes)
	if err != nil {
		t.Fatalf("Failed to read back %v", err)
	}
	if value0 != value1 {
		t.Fatalf("Wrong data %x expected %x", value1, value0)
	}
}

func TestPrint(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	rand.Seed(42)

	value := rand.Uint64()
	fmtString := "Hello %d"
	err := binlog.Log(fmtString, value)
	expected := fmt.Sprintf(fmtString, value)
	if err != nil {
		t.Fatalf("%v", err)
	}

	out, err := binlog.Print(&buf)
	if err != nil {
		t.Fatalf("%v, %v", err, out.String())
	}
	actual := out.String()
	if expected != actual {
		t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
	}
}

func BenchmarkEmptyString(b *testing.B) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello"
	binlog := Init(&buf, constDataBase, constDataSize)
	binlog.Log(fmtString)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString)
	}
}

func BenchmarkSingleInt(b *testing.B) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d"
	binlog := Init(&buf, constDataBase, constDataSize)
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, 10)
	}
}
