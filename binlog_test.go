package binlog

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/golang/glog"

	"github.com/larytet-go/moduledata"
	"github.com/larytet-go/sprintf"
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

func TestGetStrings(t *testing.T) {
	filename, err := os.Executable()
	if err != nil {
		t.Fatalf("%v", err)
	}
	bin, err := os.OpenFile(filename, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("%v", err)
	}
	f, err := elf.NewFile(bin)
	if err != nil {
		t.Fatalf("%v", err)
	}
	sectionRodataName := ".rodata"
	sectionRodata := f.Section(sectionRodataName)
	if sectionRodata == nil {
		t.Fatalf("No %s section in the ELF %s", sectionRodataName, filename)
	}
	_, err = sectionRodata.Data()
	if err != nil {
		t.Fatalf("%v", err)
	}
}

// Test if I can fetch the module names from the ELF file in this
// environment
func T1estGetModules(t *testing.T) {
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
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
	binlog.Log("Hello %d", 10)
}

func TestInt(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, &WriterControlDummy{}, uint(constDataBase), uint(constDataSize))
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
		if h.Args.fmtString != fmtString {
			t.Fatalf("Wrong format string '%s' instead of '%s' in the cache", h.Args.fmtString, fmtString)
		}
	}
}

func testPrint(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, &WriterControlDummy{}, uint(constDataBase), uint(constDataSize))
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
	binlog := Init(&buf, &WriterControlDummy{}, uint(constDataBase), uint(constDataSize))

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

func TestPrintString(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, &WriterControlDummy{}, uint(constDataBase), uint(constDataSize))

	fmtString := "Hello %s"
	arg := "world"
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

func TestPrint2Ints(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, &WriterControlDummy{}, uint(constDataBase), uint(constDataSize))
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

func BenchmarkFmtGlog(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		glog.Infof("")
	}
}

func BenchmarkFmtLog(b *testing.B) {
	logger := log.New(os.Stdout, "", 0)
	f, _ := os.Create("/dev/null")
	defer f.Close()
	logger.SetOutput(f)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Printf("")
	}
}

func BenchmarkFmtSprintf(b *testing.B) {
	for i := 0; i < b.N; i++ {
		s := fmt.Sprintf("")
		if s != "" {
			b.Fatalf("Should be empty")
		}
	}
}

func BenchmarkFmtSprintfConstInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fmt.Sprintf("%d", 0)
	}
}

func BenchmarkFmtSprintfInt(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fmt.Sprintf("%d", i)
	}
}

func BenchmarkFmtSprintfInt2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fmt.Sprintf("%d %d", i, i+1)
	}
}

func BenchmarkFmtSprintfInt4(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fmt.Sprintf("%d %d %d %d", i, i+1, i+2, i+3)
	}
}

func benchmarkMap(b *testing.B, size int) {
	m := make(map[int]string)
	for i := 0; i < size; i++ {
		m[i] = fmt.Sprintf("%d", i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := m[i%size]; !ok {
			b.Fatalf("Key %d not found in the map", i)
		}
	}
}

func BenchmarkMap(b *testing.B) {
	benchmarks := []struct {
		name string
		size int
	}{
		{"Map 1K", 1000},
		{"Map 10K", 10 * 1000},
		{"Map 100K", 100 * 1000},
		{"Map 1M", 1 * 1000 * 1000},
		{"Map 10M", 10 * 1000 * 1000},
		{"Map 20M", 20 * 1000 * 1000},
		{"Map 50M", 50 * 1000 * 1000},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			benchmarkMap(b, bm.size)
		})
	}
}

func BenchmarkEmptyString(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello"
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
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
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
	// Cache the first entry
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, 10)
	}
	b.StopTimer()
}

// Force Go compiler to allocate an object every time
// binlog.Log() is called. Performance suffers.
// Go allocates integers from the heap?
func BenchmarkSingleIntRogerPeppe(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d"
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
	// Cache the first entry
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, i)
	}
	b.StopTimer()
}

func BenchmarkSingleIntL2Cache(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
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
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
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
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
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
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
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
	binlog := Init(&buf, &WriterControlDummy{}, uint(constDataBase), uint(constDataSize))
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

func logFrintf(ioWriter io.Writer, level int, fmtStr string, args ...interface{}) {
	copy(args[2:], args[0:])
	args[0] = level
	args[1] = time.Now()
	fmtStr = "%d %v " + fmtStr
	fmt.Fprintf(ioWriter, fmtStr, args...)
}

func BenchmarkLogFprintf(b *testing.B) {
	var buf DummyIoWriter
	for i := 0; i < b.N; i++ {
		logFrintf(&buf, 0, "%d %d %d %d", 0, 1, 2, 3)
	}
}

type FieldType uint8

type Field struct {
	Key       string
	Type      FieldType
	Integer   int64
	String    string
	Interface interface{}
}

const (
	// UnknownType is the default field type. Attempting to add it to an encoder will panic.
	UnknownType FieldType = iota
	// Int64Type indicates that the field carries an int64.
	Uint64Type
)

func Uint64(key string, val uint64) Field {
	return Field{Key: key, Type: Uint64Type, Integer: int64(val)}
}

func handleFields(s string, fields ...Field) {

}

func BenchmarkZapApi(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handleFields("Hello ",
			Uint64("world", 0),
			Uint64("world", 1),
			Uint64("world", 2),
		)
	}
	b.StopTimer()
}

type T int

func (t T) Value() int {
	return int(t)
}

type I interface {
	Value() int
}

//go:noinline
func Assert(c *int, d interface{}) {
	*c += d.(int)
}

func BenchmarkAssertion(b *testing.B) {
	count := 0
	d := (interface{})(1)
	for i := 0; i < b.N; i++ {
		Assert(&count, d)
	}
}

//go:noinline
func AssertOK(c *int, d interface{}) {
	δ, _ := d.(int)
	*c += δ
}

func BenchmarkAssertionOK(b *testing.B) {
	count := 0
	d := (interface{})(1)
	for i := 0; i < b.N; i++ {
		AssertOK(&count, d)
	}
}

//go:noinline
func Bare(c *int, d int) {
	*c += d
}

func BenchmarkBare(b *testing.B) {
	count := 0
	d := 1
	for i := 0; i < b.N; i++ {
		Bare(&count, d)
	}
}

//go:noinline
func Iface(c *int, d I) {
	*c += d.Value()
}

func BenchmarkIface(b *testing.B) {
	count := 0
	d := T(1)
	for i := 0; i < b.N; i++ {
		Iface(&count, d)
	}
}

//go:noinline
func Reflect(c *int, d interface{}) {
	*c += int(reflect.ValueOf(d).Int())
}

func BenchmarkReflect(b *testing.B) {
	count := 0
	d := (interface{})(1)
	for i := 0; i < b.N; i++ {
		Reflect(&count, d)
	}
}

func BenchmarkTypeCast0(b *testing.B) {
	count := 0
	for i := 0; i < b.N; i++ {
		delta := i
		count = count + delta
	}
}

func BenchmarkTypeCast1(b *testing.B) {
	var delta interface{}
	delta = b.N
	count := 0
	for i := 0; i < b.N; i++ {
		count = count + delta.(int)
	}
}

func BenchmarkTypeCast2(b *testing.B) {
	var delta interface{}
	delta = b.N
	count := 0
	for i := 0; i < b.N; i++ {
		data := unsafe.Pointer((((*iface)(unsafe.Pointer(&delta))).data))
		count = count + *((*int)(data))
	}
}
