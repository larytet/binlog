package binlog

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime"
	//	"github.com/larytet/sprintf"
	"math/rand"
	"reflect"
	"strings"
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

func TestStringDedup(t *testing.T) {
	if ADD_SOURCE_LINE {
		s0 := "Hello, world"
		s1 := "Hello, world"
		p0 := uintptr(unsafe.Pointer(&s0))
		p1 := uintptr(unsafe.Pointer(&s1))
		if p1 == p0 {
			t.Fatalf("Golinker dedups strings in this environment")
		}
	}
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
	// This line should be placed right before call to Log()
	_, filename, line, _ := runtime.Caller(0)
	binlog.Log(fmtString, value0)
	var hash uint32
	err := binary.Read(&buf, binary.LittleEndian, &hash)
	if err != nil {
		t.Fatalf("Failed to read back hash %v", err)
	}
	if SEND_STRING_INDEX {
		var index uint32
		err = binary.Read(&buf, binary.LittleEndian, &index)
		if err != nil {
			t.Fatalf("Failed to read back index %v", err)
		}
		if index != 1 {
			t.Fatalf("Index is %d instead of 1", index)
		}
	}
	if ADD_SOURCE_LINE {
		var filenameHash uint16
		var lineNumber uint16
		err = binary.Read(&buf, binary.LittleEndian, &filenameHash)
		if err == nil {
			err = binary.Read(&buf, binary.LittleEndian, &lineNumber)
		}
		if err != nil {
			t.Fatalf("Failed to read filename %v", err)
		}
		expectedFilenameHash := uint16(md5sum(filename))
		expectedLineNumber := uint16(line + 1)
		if filenameHash != expectedFilenameHash {
			t.Fatalf("Filename hash is %x instead of %x for file '%s'", filenameHash, expectedFilenameHash, filename)
		}
		if lineNumber != expectedLineNumber {
			t.Fatalf("Line number is %d instead of %d for file '%s'", lineNumber, expectedLineNumber, filename)
		}
	}
	var value1 int32
	err = binary.Read(&buf, binary.LittleEndian, &value1) // bytes.NewBuffer(bufBytes)
	if err != nil {
		t.Fatalf("Failed to read back %v", err)
	}
	if value0 != value1 {
		t.Fatalf("Wrong data %x expected %x", value1, value0)
	}
	indexTable, filenames := binlog.GetIndexTable()
	if len(indexTable) != 1 {
		t.Fatalf("Wrong size of the index table %d expected %d", len(indexTable), 1)
	}
	if ADD_SOURCE_LINE && len(filenames) != 1 {
		t.Fatalf("Wrong size of the filenames %d expected %d", len(filenames), 1)
	}
	if !ADD_SOURCE_LINE && len(filenames) != 0 {
		t.Fatalf("Wrong size of the filenames %d expected %d", len(filenames), 0)
	}
	for _, h := range indexTable {
		if h.FmtString != fmtString {
			t.Fatalf("Wrong format string '%s' instead of '%s' in the cache", h.FmtString, fmtString)
		}
	}
}

func testPrint(t *testing.T) {
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
	if ADD_SOURCE_LINE {
		if !strings.HasSuffix(actual, expected) {
			t.Fatalf("Print failed '%s' does not contain '%s' ", actual, expected)
		}
	} else {
		if expected != actual {
			t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
		}

	}
}

type testParameters struct {
	sendStringIndex bool
	addSourceLine   bool
}

func TestPrint(t *testing.T) {
	var tests []testParameters = []testParameters{
		testParameters{
			false, false,
		},
		testParameters{
			false, true,
		},
		testParameters{
			true, false,
		},
		testParameters{
			true, true,
		},
	}
	for _, p := range tests {
		SEND_STRING_INDEX = p.sendStringIndex
		ADD_SOURCE_LINE = p.addSourceLine
		testPrint(t)
	}
	SEND_STRING_INDEX = false
	ADD_SOURCE_LINE = false
}

func TestCacheL2(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	rand.Seed(42)

	value := rand.Uint64()
	// dynamic allocation here
	fmtString := fmt.Sprintf("%s %%d", "Hello")
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
	if ADD_SOURCE_LINE {
		if !strings.HasSuffix(actual, expected) {
			t.Fatalf("Print failed '%s' does not contain '%s' ", actual, expected)
		}
	} else {
		if expected != actual {
			t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
		}
	}
	statistics := binlog.GetStatistics()
	if statistics.CacheL2Used != 1 {
		t.Fatalf("Cache L2 used %d times instead of 1 time", statistics.CacheL2Used)
	}
}

type DummyIoWriter struct {
}

func (w *DummyIoWriter) Write(data []byte) (int, error) {
	return len(data), nil
}
func (w *DummyIoWriter) Grow(size int) {
}

func BenchmarkEmptyString(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello"
	binlog := Init(&buf, constDataBase, constDataSize)
	binlog.Log(fmtString)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString)
	}
	b.StopTimer()
	statistics := binlog.GetStatistics()
	if statistics.L2CacheMiss != 0 {
		b.Fatalf(" L2Cache miss is %d instead of zero", statistics.L2CacheMiss)
	}
	if statistics.L1CacheMiss != 1 {
		b.Fatalf(" L1Cache miss is %d instead of one", statistics.L1CacheMiss)
	}
	if statistics.L1CacheHit != uint64(b.N) {
		b.Fatalf(" L1Cache hist is %d instead of %d", statistics.L1CacheHit, b.N)
	}
	//b.Logf(sprintf.SprintfStructure(statistics, 4, "  %15s %9d", nil))
}

func BenchmarkSingleInt(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d"
	binlog := Init(&buf, constDataBase, constDataSize)
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, 10)
	}
}

func BenchmarkSingleIntCacheL2(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	binlog := Init(&buf, constDataBase, constDataSize)
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, 10)
	}
	b.StopTimer()
	statistics := binlog.GetStatistics()
	if statistics.L2CacheMiss != 1 {
		b.Fatalf("L2Cache miss is %d instead of 1", statistics.L2CacheMiss)
	}
	if (statistics.CacheL2Used - 1) != uint64(b.N) {
		b.Fatalf(" L2Cache used %d times instead of %d time", statistics.CacheL2Used, b.N)
	}
	if statistics.L2CacheMiss != 1 {
		b.Fatalf("L2Cache miss is %d instead of one", statistics.L1CacheMiss)
	}
}
