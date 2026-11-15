package binlog

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/golang/glog"
	//	"github.com/k8s.io/klog"
	"github.com/sirupsen/logrus"
	"go.uber.org/zap"

	"github.com/valyala/fasthttp"
)

func nanotimeNow() int64 { return time.Now().UnixNano() }

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

var s0 = "Hello, world"
var s1 = "Hello, world2"

func TestStringLocationGlobalLocalHeader(t *testing.T) {
	s1 := "Hello, world2"
	d0, d1 := getStringAddress(s0), getStringAddress(s1)
	if d0+0x100 > d1 {
		t.Fatalf("Bad locations %x %x", d0, d1)
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

// TestReadme() is a sample for the README file, not a real test
func TestReadme(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
	binlog.Log("Hello %d", 10)
}

func TestInt(t *testing.T) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := Init(&buf, &WriterControlDummy{}, constDataBase, constDataSize)
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
	addTimestamp    bool
}

func TestPrint(t *testing.T) {
	var tests = []testParameters{
		testParameters{
			false, false, true, false,
		},
		testParameters{
			false, true, false, false,
		},
		testParameters{
			false, true, true, false,
		},
		testParameters{
			true, false, false, false,
		},
		testParameters{
			true, false, true, false,
		},
		testParameters{
			true, true, false, false,
		},
		testParameters{
			true, true, true, false,
		},

		testParameters{
			false, false, true, true,
		},
		testParameters{
			false, true, false, true,
		},
		testParameters{
			false, true, true, true,
		},
		testParameters{
			true, false, false, true,
		},
		testParameters{
			true, false, true, true,
		},
		testParameters{
			true, true, false, true,
		},
		testParameters{
			true, true, true, true,
		},

		// Last test is all FALSE
		testParameters{
			false, false, false, false,
		},
	}
	for _, p := range tests {
		SEND_LOG_INDEX = p.sendLogIndex
		SEND_STRING_INDEX = p.sendStringIndex
		ADD_SOURCE_LINE = p.addSourceLine
		ADD_TIMESTAMP = p.addTimestamp
		testPrint(t)
	}
}

type testPrintIntegersParameters struct {
	arg interface{}
}

func testPrintIntegers(t *testing.T, arg interface{}) {
	var buf bytes.Buffer
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})

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
	var tests = []testPrintIntegersParameters{
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})

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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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

func init() {
}

func fastHTTPHandler(ctx *fasthttp.RequestCtx) {
	fmt.Fprintf(ctx, "Ok")
}

// Based on https://github.com/valyala/fasthttp/issues/599
func BenchmarkFastHTTP(b *testing.B) {
	go fasthttp.ListenAndServe(":8080", fastHTTPHandler)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI("http://127.0.0.1:8080")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp := fasthttp.AcquireResponse()
		if err := fasthttp.Do(req, resp); err != nil {
			b.Fatalf("%v", err)
		}
		status := resp.StatusCode()
		if status != fasthttp.StatusOK {
			b.Fatalf("Status %d", status)
		}
		fasthttp.ReleaseResponse(resp)
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

func BenchmarkFmtSprintfInt6(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fmt.Sprintf("%d %d %d %d %d %d", i, i+1, i+2, i+3, i+4, i+5)
	}
}

func BenchmarkFmtSprintfInt8(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fmt.Sprintf("%d %d %d %d %d %d %d %d", i, i+1, i+2, i+3, i+4, i+5, i+6, i+7)
	}
}

func BenchmarkZAPInt4Simple(b *testing.B) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{
		"/dev/null",
	}
	logger, _ := cfg.Build()
	sugar := logger.Sugar()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sugar.Infof("%d %d %d %d", i, i+1, i+2, i+3)
	}
	sugar.Sync()
}

func BenchmarkZAPThreads(b *testing.B) {
	//
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{
		"/dev/null",
	}
	logger, _ := cfg.Build()
	ch1, ch2 := make(chan struct{}), make(chan struct{})
	b.ResetTimer()
	f := func(ch chan struct{}) {
		for i := 0; i < b.N/2; i++ {
			logger.Error("")
		}
		close(ch)
	}
	go f(ch1)
	go f(ch2)
	<-ch1
	<-ch2
	logger.Sync()
}

/*
func BenchmarkKlog(b *testing.B) {
	flag.Set("logtostderr", "false")
	flag.Set("log_file", "/dev/null")
	flag.Parse()
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	f, _ := os.Create("/dev/null")
	defer f.Close()
	klog.SetOutput(f)

	// Sync the glog and klog flags.
	flag.CommandLine.VisitAll(func(f1 *flag.Flag) {
		f2 := klogFlags.Lookup(f1.Name)
		if f2 != nil {
			value := f1.Value.String()
			f2.Value.Set(value)
		}
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		klog.Infof("")
	}
}
*/

func BenchmarkZAPConstInt(b *testing.B) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{
		"/dev/null",
	}
	logger, _ := cfg.Build()
	constInt := zap.Int("", 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Error("", constInt)
	}
	logger.Sync()
}

func BenchmarkZAP(b *testing.B) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{
		"/dev/null",
	}
	logger, _ := cfg.Build()
	logger.Sync()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Error("")
	}
}

func BenchmarkBinLogConstInt(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binlog.Log("Hello %d", 0)
	}
}

func BenchmarkBinLogConstIntFullFeatured(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, TimestampDummy})
	SEND_LOG_INDEX, SEND_STRING_INDEX, ADD_SOURCE_LINE, ADD_TIMESTAMP = false, false, true, true

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		binlog.Log("Hello %d", 0)
	}
}

func BenchmarkGlog(b *testing.B) {
	flag.Parse()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		glog.Infof("")
	}
	glog.Flush()
}

func BenchmarkBinLogL2Cache(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := fmt.Sprintf("%s %%d", "Hello")
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
	// Cache the first entry
	binlog.Log(fmtString, 10)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, 10)
	}
}

func BenchmarkBinLogEmptyString(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello"
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
	// Cache the first entry
	binlog.Log(fmtString)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString)
	}
}

func BenchmarkBinLogInt4(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8 + 8 + 8 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d %d %d %d"
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
	// Cache the first entry
	binlog.Log(fmtString, 0, 1, 2, 3)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		binlog.Log(fmtString, i, i+1, i+2, i+3)
	}
}

func BenchmarkFmtPrintf(b *testing.B) {
	f, _ := os.Create("/dev/null")
	defer f.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fmt.Fprintf(f, "")
	}
}

func BenchmarkZAPInt4(b *testing.B) {
	cfg := zap.NewProductionConfig()
	cfg.OutputPaths = []string{
		"/dev/null",
	}
	logger, _ := cfg.Build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		logger.Error("", zap.Int("", i), zap.Int("", i+1), zap.Int("", i+2), zap.Int("", i+3))
	}
	logger.Sync()
}

func BenchmarkLogrus(b *testing.B) {
	logrus.SetFormatter(&logrus.JSONFormatter{
		DisableTimestamp: true,
		PrettyPrint:      false,
	})
	contextLogger := logrus.WithFields(logrus.Fields{})
	f, _ := os.Create("/dev/null")
	defer f.Close()
	logrus.SetOutput(f)
	logrus.SetLevel(logrus.DebugLevel)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		contextLogger.Debug("")
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
	//b.Logf(fmt.Sprintf("%+v", statistics))
}

// Force Go compiler to allocate an object every time
// binlog.Log() is called. Performance suffers.
// Go allocates integers from the heap?
func BenchmarkSingleIntRogerPeppe(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d"
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
		b.Logf("\n%s\n\n", fmt.Sprintf("%+v", statistics))
	}
}

func Benchmark3IntsLogIndex(b *testing.B) {
	var buf DummyIoWriter
	buf.Grow(b.N * (8 + 4 + 4 + 8))
	constDataBase, constDataSize := GetSelfTextAddressSize()
	fmtString := "Hello %d %d %d"
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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
	binlog := New(Config{&buf, &WriterControlDummy{}, constDataBase, constDataSize, nanotimeNow})
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

// FR-LOG-11 (D10): %% is a literal percent sign and takes no argument
func TestParseLogLinePercent(t *testing.T) {
	hArgs, err := parseLogLine("100%% sure %d", []interface{}{7})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(hArgs) != 1 || hArgs[0].fmtVerb != 'd' {
		t.Fatalf("expected one %%d argument, got %d", len(hArgs))
	}
	if hArgs, err = parseLogLine("%%%%", nil); err != nil || len(hArgs) != 0 {
		t.Fatalf("'%%%%%%%%' must have no arguments: %v %v", hArgs, err)
	}
}

// D15: fewer arguments than verbs is an error, not a panic
func TestParseLogLineMissingArgs(t *testing.T) {
	if _, err := parseLogLine("%d %d", []interface{}{1}); err == nil {
		t.Fatalf("expected an error for a missing argument")
	}
}

// FR-LOG-3 (D11): signed integers keep their sign through the stream
func TestSignedRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		log  func(b *Binlog) error
		want string
	}{
		{"int8", func(b *Binlog) error { return b.Log("signed int8 %d", int8(-128)) }, "-128"},
		{"int16", func(b *Binlog) error { return b.Log("signed int16 %d", int16(-32768)) }, "-32768"},
		{"int32", func(b *Binlog) error { return b.Log("signed int32 %d", int32(-2147483648)) }, "-2147483648"},
		{"int64", func(b *Binlog) error { return b.Log("signed int64 %d", int64(-9223372036854775808)) }, "-9223372036854775808"},
		{"int", func(b *Binlog) error { return b.Log("signed int %d", -1) }, "-1"},
		{"uint8", func(b *Binlog) error { return b.Log("unsigned uint8 %d", uint8(255)) }, "255"},
	}
	for _, c := range cases {
		var buf bytes.Buffer
		base, size := GetSelfTextAddressSize()
		b := Init(&buf, &WriterControlDummy{}, base, size)
		if err := c.log(b); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		entry, err := b.DecodeNext(&buf)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := fmt.Sprintf("%d", entry.Args[0]); got != c.want {
			t.Errorf("%s: decoded %s, want %s", c.name, got, c.want)
		}
	}
}

// FR-LOG-4 (D12): an empty string argument round trips
func TestEmptyString(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	if err := b.Log("empty [%s] end", ""); err != nil {
		t.Fatalf("%v", err)
	}
	entry, err := b.DecodeNext(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got := fmt.Sprintf(entry.FmtString, entry.Args...); got != "empty [] end" {
		t.Fatalf("decoded %q", got)
	}
	// FR-DEC-1: an exhausted stream is io.EOF, a cut entry is an error
	if _, err := b.DecodeNext(&buf); err != io.EOF {
		t.Fatalf("expected io.EOF at the end of the stream, got %v", err)
	}
}

// FR-LOG-4 (D13): a string longer than 65535 bytes is not truncated
func TestLongString(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	long := strings.Repeat("z", 70000)
	if err := b.Log("long [%s]", long); err != nil {
		t.Fatalf("%v", err)
	}
	entry, err := b.DecodeNext(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got := fmt.Sprintf(entry.FmtString, entry.Args...); got != "long ["+long+"]" {
		t.Fatalf("decoded %d bytes", len(got))
	}
}

type recordingControl struct{ events []string }

func (r *recordingControl) FrameStart(io.Writer) { r.events = append(r.events, "start") }
func (r *recordingControl) FrameEnd(io.Writer)   { r.events = append(r.events, "end") }

// failingWriter fails the Nth Write (counting from 1) and every one after it
type failingWriter struct{ n, calls int }

func (w *failingWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.n {
		return 0, fmt.Errorf("disk full")
	}
	return len(p), nil
}

// TC-LOG-4 (FR-LOG-5): FrameStart and FrameEnd surround every entry
func TestFrameCallbacks(t *testing.T) {
	var buf bytes.Buffer
	ctl := &recordingControl{}
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, ctl, base, size)
	for i := 0; i < 3; i++ {
		if err := b.Log("frame %d", i); err != nil {
			t.Fatalf("%v", err)
		}
	}
	if got := strings.Join(ctl.events, ","); got != "start,end,start,end,start,end" {
		t.Fatalf("events %s", got)
	}
}

// TC-LOG-9 (D1): a write error is returned and the frame is still closed
func TestWriteErrorReturned(t *testing.T) {
	for n := 1; n <= 2; n++ { // the hash write, then the argument write
		ctl := &recordingControl{}
		base, size := GetSelfTextAddressSize()
		b := Init(&failingWriter{n: n}, ctl, base, size)
		if err := b.Log("fail %d", 5); err == nil {
			t.Fatalf("failing Write #%d: Log returned nil", n)
		}
		if got := strings.Join(ctl.events, ","); got != "start,end" {
			t.Fatalf("failing Write #%d: events %s", n, got)
		}
	}
}

// TC-LOG-8, TC-LOG-10 (FR-LOG-9, D9): bad calls return an error, write nothing, are not cached
func TestBadCallsWriteNothing(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	if err := b.Log("verb %v", 1); err == nil {
		t.Fatalf("%%v must be rejected")
	}
	if err := b.Log("verb %p", 1); err == nil {
		t.Fatalf("%%p must be rejected")
	}
	if err := b.Log("two %d %d", 1); err == nil {
		t.Fatalf("missing argument must be rejected")
	}
	if buf.Len() != 0 {
		t.Fatalf("%d bytes written by rejected calls", buf.Len())
	}
	if len(b.handlersLookupByHash) != 0 {
		t.Fatalf("a rejected call was cached")
	}
}

// D16: two literals in the same 8-byte slot of the L1 cache must not share a handler.
// The literals are 7 bytes long, so the linker packs them next to each other.
func TestL1SlotCollision(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	lits := []string{"cola %d", "colb %d", "colc %d", "cold %d", "cole %d", "colf %d", "colg %d", "colh %d"}
	for round := 0; round < 3; round++ {
		for i, lit := range lits {
			buf.Reset()
			var err error
			switch i {
			case 0:
				err = b.Log("cola %d", i)
			case 1:
				err = b.Log("colb %d", i)
			case 2:
				err = b.Log("colc %d", i)
			case 3:
				err = b.Log("cold %d", i)
			case 4:
				err = b.Log("cole %d", i)
			case 5:
				err = b.Log("colf %d", i)
			case 6:
				err = b.Log("colg %d", i)
			case 7:
				err = b.Log("colh %d", i)
			}
			if err != nil {
				t.Fatalf("%v", err)
			}
			entry, err := b.DecodeNext(&buf)
			if err != nil {
				t.Fatalf("%v", err)
			}
			if got, want := fmt.Sprintf(entry.FmtString, entry.Args...), fmt.Sprintf(lit, i); got != want {
				t.Fatalf("round %d: decoded %q, want %q", round, got, want)
			}
		}
	}
}

// TC-LOG-6 (FR-LOG-7): floats round trip bit for bit
func TestFloatRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	f64s := []float64{0, math.Copysign(0, -1), 1.5, -2.25, math.Pi, math.MaxFloat64, math.SmallestNonzeroFloat64, math.Inf(1), math.Inf(-1), math.NaN()}
	for _, v := range f64s {
		buf.Reset()
		if err := b.Log("f64 %g", v); err != nil {
			t.Fatalf("%v", err)
		}
		entry, err := b.DecodeNext(&buf)
		if err != nil {
			t.Fatalf("%v", err)
		}
		got, ok := entry.Args[0].(float64)
		if !ok || math.Float64bits(got) != math.Float64bits(v) {
			t.Errorf("float64 %v decoded as %v (%T)", v, entry.Args[0], entry.Args[0])
		}
	}
	f32s := []float32{0, float32(math.Copysign(0, -1)), 1.5, math.MaxFloat32, math.SmallestNonzeroFloat32, float32(math.Inf(-1)), float32(math.NaN())}
	for _, v := range f32s {
		buf.Reset()
		if err := b.Log("f32 %f", v); err != nil {
			t.Fatalf("%v", err)
		}
		entry, err := b.DecodeNext(&buf)
		if err != nil {
			t.Fatalf("%v", err)
		}
		got, ok := entry.Args[0].(float32)
		if !ok || math.Float32bits(got) != math.Float32bits(v) {
			t.Errorf("float32 %v decoded as %v (%T)", v, entry.Args[0], entry.Args[0])
		}
	}
}

// TC-LOG-7 (FR-LOG-8): %c with runes
func TestCharRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	for _, r := range []rune{'a', '世', 0x10FFFF} {
		buf.Reset()
		if err := b.Log("char %c", r); err != nil {
			t.Fatalf("%v", err)
		}
		entry, err := b.DecodeNext(&buf)
		if err != nil {
			t.Fatalf("%v", err)
		}
		if got, want := fmt.Sprintf(entry.FmtString, entry.Args...), fmt.Sprintf("char %c", r); got != want {
			t.Errorf("decoded %q, want %q", got, want)
		}
	}
}

// FR-LOG-9: a verb that does not fit the argument type is an error and writes nothing
func TestVerbTypeMismatch(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	if err := b.Log("mismatch %d", 1.5); err == nil {
		t.Errorf("%%d with a float must fail")
	}
	if err := b.Log("mismatch2 %f", 1); err == nil {
		t.Errorf("%%f with an int must fail")
	}
	if err := b.Log("mismatch3 %s", 1); err == nil {
		t.Errorf("%%s with an int must fail")
	}
	if buf.Len() != 0 {
		t.Errorf("%d bytes written", buf.Len())
	}
}
