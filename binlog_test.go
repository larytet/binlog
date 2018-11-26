package binlog

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/larytet-go/moduledata"
	"github.com/larytet-go/sprintf"
	"math/rand"
	"os"
	"reflect"
	"runtime"
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

func executableContains(filename string, moduleName string) (bool, error) {
	filename, err := os.Executable()
	if err != nil {
		return false, err
	}
	if modules, err := moduledata.GetModules(filename); err == nil {
		for _, m := range modules {
			if strings.HasSuffix(m, moduleName) {
				return true, nil
			}
		}
		return false, nil
	} else {
		return false, err
	}

}

// Test if I can fetch the module names from the ELF file in this
// environment
func TestGetModules(t *testing.T) {
	filename, err := os.Executable()
	if err != nil {
		t.Fatalf("%v", err)
	}
	modulename := "binlog_test.go"
	contains, err := executableContains(filename, modulename)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !contains {
		t.Logf("Module %s not found in %s", modulename, filename)
	}
}

func TestGetIndexTable(t *testing.T) {
	filename, err := os.Executable()
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _, err = GetIndexTable(filename)
	if err != nil {
		t.Fatalf("%v", err)
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
	binlog := Init(&buf, constDataBase, constDataSize)
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
	_, filename, line, _ := runtime.Caller(0)
	err := binlog.Log(fmtString, value)
	expected := fmt.Sprintf(fmtString, value)
	if err != nil {
		t.Fatalf("%v", err)
	}

	logEntry, err := binlog.DecodeNext(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	actual := fmt.Sprintf(logEntry.FmtString, logEntry.Args...)
	if expected != actual {
		t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
	}

	if ADD_SOURCE_LINE {
		if logEntry.Filename != filename {
			t.Fatalf("Filename is '%s', instead of '%s'", logEntry.Filename, filename)
		}
		if logEntry.LineNumber != (line + 1) {
			t.Fatalf("Linenumber is '%d', instead of '%d'", logEntry.LineNumber, line+1)
		}
	}
}

type testParameters struct {
	sendLogIndex    bool
	sendStringIndex bool
	addSourceLine   bool
}

func TestPrint(t *testing.T) {
	var tests []testParameters = []testParameters{
		testParameters{
			false, false, true,
		},
		testParameters{
			false, true, false,
		},
		testParameters{
			false, true, true,
		},
		testParameters{
			true, false, false,
		},
		testParameters{
			true, false, true,
		},
		testParameters{
			true, true, false,
		},
		testParameters{
			true, true, true,
		},

		// Last test is all FALSE
		testParameters{
			false, false, false,
		},
	}
	for _, p := range tests {
		SEND_LOG_INDEX = p.sendLogIndex
		SEND_STRING_INDEX = p.sendStringIndex
		ADD_SOURCE_LINE = p.addSourceLine
		testPrint(t)
	}
}

type testPrintIntegersParameters struct {
	arg interface{}
}

func testPrintIntegers(t *testing.T, arg interface{}) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))

	fmtString := "Hello %d"
	err := binlog.Log(fmtString, arg)
	expected := fmt.Sprintf(fmtString, arg)
	if err != nil {
		t.Fatalf("%v", err)
	}

	logEntry, err := binlog.DecodeNext(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	actual := fmt.Sprintf(logEntry.FmtString, logEntry.Args...)
	if expected != actual {
		t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
	}
}

func TestPrintIntegers(t *testing.T) {
	var tests []testPrintIntegersParameters = []testPrintIntegersParameters{
		testPrintIntegersParameters{
			uint8(5),
		},
		testPrintIntegersParameters{
			uint16(5),
		},
		testPrintIntegersParameters{
			uint32(5),
		},
		testPrintIntegersParameters{
			uint64(5),
		},
		testPrintIntegersParameters{
			int8(5),
		},
		testPrintIntegersParameters{
			int16(5),
		},
		testPrintIntegersParameters{
			int32(5),
		},
		testPrintIntegersParameters{
			int64(5),
		},
		testPrintIntegersParameters{
			uint(5),
		},
		testPrintIntegersParameters{
			int(5),
		},
	}
	for _, p := range tests {
		testPrintIntegers(t, p.arg)
	}
}

func TestPrint2Ints(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	rand.Seed(42)

	value := rand.Uint64()
	fmtString := "Hello %d %d"
	_, filename, line, _ := runtime.Caller(0)
	err := binlog.Log(fmtString, value, value)
	expected := fmt.Sprintf(fmtString, value, value)
	if err != nil {
		t.Fatalf("%v", err)
	}

	logEntry, err := binlog.DecodeNext(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	actual := fmt.Sprintf(logEntry.FmtString, logEntry.Args...)
	if expected != actual {
		t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
	}

	if ADD_SOURCE_LINE {
		if logEntry.Filename != filename {
			t.Fatalf("Filename is '%s', instead of '%s'", logEntry.Filename, filename)
		}
		if logEntry.LineNumber != (line + 1) {
			t.Fatalf("Linenumber is '%d', instead of '%d'", logEntry.LineNumber, line+1)
		}
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
	// Cache the first entry
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
	// Cache the first entry
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, 10)
	}
	b.StopTimer()
}

func BenchmarkSingleIntL2Cache(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	binlog := Init(&buf, constDataBase, constDataSize)
	// Cache the first entry
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
	if (statistics.L2CacheUsed - 1) != uint64(b.N) {
		b.Fatalf(" L2Cache used %d times instead of %d time", statistics.L2CacheUsed, b.N)
	}
	if statistics.L2CacheMiss != 1 {
		b.Fatalf("L2Cache miss is %d instead of one", statistics.L1CacheMiss)
	}
}

func Benchmark2Ints(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d %d"
	binlog := Init(&buf, constDataBase, constDataSize)
	args := []interface{}{10, 20}
	// Cache the first entry
	binlog.Log(fmtString, args...)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, args...)
	}
	b.StopTimer()
}

func Benchmark3Ints(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d %d %d"
	binlog := Init(&buf, constDataBase, constDataSize)
	args := []interface{}{10, 20, 30}
	// Cache the first entry
	binlog.Log(fmtString, args...)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, args...)
	}
	b.StopTimer()
	if b.N >= 20000000 {
		statistics := binlog.GetStatistics()
		b.Logf("\n%s\n\n", sprintf.SprintfStructure(statistics, 4, "  %15s %9d", nil))
	}
}

func Benchmark3IntsLogIndex(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d %d %d"
	binlog := Init(&buf, constDataBase, constDataSize)
	args := []interface{}{10, 20, 30}
	// Cache the first entry
	binlog.Log(fmtString, args...)
	SEND_LOG_INDEX = true
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, args...)
	}
	b.StopTimer()
	SEND_LOG_INDEX = false
}

func TestL2Cache(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, uint(constDataBase), uint(constDataSize))
	rand.Seed(42)

	value := rand.Uint64()
	// dynamic allocation here
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	_, filename, line, _ := runtime.Caller(0)
	err := binlog.Log(fmtString, value)
	expected := fmt.Sprintf(fmtString, value)
	if err != nil {
		t.Fatalf("%v", err)
	}

	logEntry, err := binlog.DecodeNext(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}

	actual := fmt.Sprintf(logEntry.FmtString, logEntry.Args...)
	if expected != actual {
		t.Fatalf("Print failed expected '%s', actual '%s'", expected, actual)
	}

	if ADD_SOURCE_LINE {
		if logEntry.Filename != filename {
			t.Fatalf("Filename is '%s', instead of '%s'", logEntry.Filename, filename)
		}
		if logEntry.LineNumber != (line + 1) {
			t.Fatalf("Linenumber is '%d', instead of '%d'", logEntry.LineNumber, line+1)
		}
	}
	statistics := binlog.GetStatistics()
	if statistics.L2CacheUsed != 1 {
		t.Fatalf("Cache L2 used %d times instead of 1 time", statistics.L2CacheUsed)
	}
}

func BenchmarkFmtFprintf3Ints(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	fmtString := "Hello %d %d %d"
	args := []interface{}{10, 20, 30}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		fmt.Fprintf(&buf, fmtString, args...)
	}
	b.StopTimer()
}

func BenchmarkFmtSprintf3Ints(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	fmtString := "Hello %d %d %d"
	args := []interface{}{10, 20, 30}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		s := fmt.Sprintf(fmtString, args...)
		buf.Write([]byte(s))
	}
	b.StopTimer()
}
