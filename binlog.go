package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"runtime"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"

	"github.com/larytet-go/procfs"
	"github.com/larytet-go/procfs/maps"
)

//import "C"

// SEND_LOG_INDEX enables unique system level running counter of logs
var SEND_LOG_INDEX = false

// SEND_STRING_INDEX enables sending the index of the format string
// I keep hash of the format string and index of the string in the
// cache. When I decode the binary stream I can ensure that both 32 bits hash
// and index of the string match. This flag is useful for debug or fast lookup
// when decoding binary streams
var SEND_STRING_INDEX = false

// ADD_SOURCE_LINE enables adding hash of the filename (16 bits)
// and line in the source (16 bits) to the binary stream
// I assume the the goloang does not dedups the constant strings and all
// calls to the Log() use unique string. There is a test which ensures this
var ADD_SOURCE_LINE = false

// ADD_TIMESTAMP enables timestamping of the log messages
var ADD_TIMESTAMP = false

var binlogIndex uint64

type DecodeArg struct {
	argType reflect.Type // type of the argument
	argKind reflect.Kind // "kind" of the argument, for example int32
}

type HandlerArg struct {
	writer    writer
	fmtVerb   rune // for example, x (from %x)
	decodeArg DecodeArg
}

type FormatArgs struct {
	fmtString string        // the format string itself for decoding
	args      []*HandlerArg // list of functions to output the data correctly 1,4 or 8 bytes of integer
}

// WriterControl can be empty, like in WriterControlDummy
// Log() will call FrameStart()/FrameStart() for every log entry
type WriterControl interface {
	FrameStart(io.Writer)
	FrameEnd(io.Writer)
}

type WriterControlDummy struct {
}

func (*WriterControlDummy) FrameStart(io.Writer) {
}

func (*WriterControlDummy) FrameEnd(io.Writer) {
}

type Handler struct {
	Args             FormatArgs
	Address          uintptr // address of the string
	IsL1Cache        bool    // true if the string in the L1 cache
	HashUint         uint32  // hash of the format string
	IndexUint        uint32  // hash of the format string
	FilenameHashUint uint16  // hash of the filename
	LineNumberUint   uint16  // source line number

	// I can output only byte slices, therefore I keep slices
	index        []byte // a running index of the handler
	hash         []byte // hash of the format string
	filenameHash []byte
	lineNumber   []byte
}

type Statistics struct {
	L1CacheMiss    uint64
	L2CacheMiss    uint64
	L1CacheHit     uint64
	L2CacheHit     uint64
	L2CacheUsed    uint64
	StringOffsetOk uint64
	StringOOM      uint64
}

type Config struct {
	IOWriter      io.Writer
	WriterControl WriterControl
	ConstDataBase uint
	ConstDataSize uint
	Timestamp     func() int64
}

type Binlog struct {
	config       Config
	currentIndex uint32

	// Index in this array is a virtual address of the format string
	// This is for fast lookup of constant strings from the executable
	// code section
	L1Cache []*Handler

	// Index in this array is the string itself
	// I need this map for lookup of strings which address is not
	// part of the executable code section
	// L2Cache is 8x slower than L1Cache in the benchmark
	L2Cache map[string]*Handler

	// All filenames I encountered
	// Only if ADD_SOURCE_LINE is true
	Filenames map[uint16]string

	// This is map[format string hash]*Handler
	// I need this map for decoding of the binary stream
	handlersLookupByHash map[uint32]*Handler
	statistics           Statistics
}

// ALIGNMENT is the size of a pointer in the data section
const ALIGNMENT uint = 8

func TimestampDummy() int64 {
	return 0
}

// Init is depreciated, use New() instead
func Init(ioWriter io.Writer, writerControl WriterControl, constDataBase uint, constDataSize uint) *Binlog {
	return New(Config{ioWriter, writerControl, constDataBase, constDataSize, TimestampDummy})
}

// New returns a new instance of the logger
// constDataBase is an address of the initialzied const data, constDataSize is it's size
func New(config Config) *Binlog {
	// allocate one handler more for handling default cases
	config.ConstDataSize = config.ConstDataSize / ALIGNMENT
	L1Cache := make([]*Handler, config.ConstDataSize+1)
	L2Cache := make(map[string]*Handler)
	filenames := make(map[uint16]string)
	handlersLookupByHash := make(map[uint32]*Handler)
	binlog := &Binlog{
		config:               config,
		L1Cache:              L1Cache,
		L2Cache:              L2Cache,
		Filenames:            filenames,
		handlersLookupByHash: handlersLookupByHash,
	}
	return binlog
}

func (b *Binlog) GetStatistics() Statistics {
	return b.statistics
}

// Log is similar to fmt.Fprintf(b.config.IOWriter, fmtStr, args)
func (b *Binlog) Log(fmtStr string, args ...interface{}) error {
	h, err := b.getHandler(fmtStr, args)
	if err != nil {
		return err
	}

	hArgs := h.Args.args
	if len(hArgs) != len(args) {
		return fmt.Errorf("Number of args %d does not match log line %d", len(args), len(hArgs))
	}
	b.config.WriterControl.FrameStart(b.config.IOWriter)
	b.config.IOWriter.Write(h.hash)

	if SEND_STRING_INDEX {
		b.config.IOWriter.Write(h.index)
	}

	if ADD_SOURCE_LINE {
		b.config.IOWriter.Write(h.filenameHash)
		b.config.IOWriter.Write(h.lineNumber)
	}

	if SEND_LOG_INDEX {
		logIndex := atomic.AddUint64(&binlogIndex, 1)
		writer := writerByteArray{count: 8}
		(&writer).write(b.config.IOWriter, unsafe.Pointer(&logIndex))
	}
	if ADD_TIMESTAMP {
		timestamp := b.config.Timestamp()
		writer := writerByteArray{count: 8}
		(&writer).write(b.config.IOWriter, unsafe.Pointer(&timestamp))
	}

	for i, arg := range args {
		hArg := h.Args.args[i]
		writer := hArg.writer
		if err := b.writeArgumentToOutput(writer, arg); err != nil {
			err = fmt.Errorf("Failed to write value %v", err)
			break
		}
	}
	b.config.WriterControl.FrameEnd(b.config.IOWriter)
	return err
}

type LogEntry struct {
	Filename   string
	LineNumber int
	FmtString  string
	Args       []interface{}
	Index      uint64
	Timestamp  int64
}

// DecodeNext converts one record from the binary stream to a human readable format
func (b *Binlog) DecodeNext(reader io.Reader) (*LogEntry, error) {
	indexTable, filenames := b.GetIndexTable()
	return DecodeNext(reader, indexTable, filenames)
}

// DecodeNext converts one record from the binary stream to a human readable format
// You want to fmt.Sprintf(LogEntry.fmtString, LogEntry.args)
// This API is slow - relies heavily on reflection, allocates strings and slices.
// The idea is that I will not call this API often, and when I call the API I
// will have a serious machine dedicated to the the task
//
// Decoding of the binary log is a three steps process:
// 1. Read 4 bytes hash from the stream
// 2. find the format string and arguments in the L1 or L2 cache
// 3. Read arguments from the binary stream
func DecodeNext(reader io.Reader, indexTable map[uint32]*Handler, filenames map[uint16]string) (*LogEntry, error) {
	var logEntry = &LogEntry{}
	var h *Handler
	// Read format string hash
	if hashUint, err := readIntegerFromReader(reader, 4); err == nil {
		var ok bool
		if h, ok = indexTable[uint32(hashUint)]; !ok {
			return nil, fmt.Errorf("Failed to find format string hash %x", hashUint)
		}
	} else {
		return nil, err
	}

	if SEND_STRING_INDEX {
		// Read format string index
		if index, err := readIntegerFromReader(reader, 4); err == nil {
			if uint32(index) != h.IndexUint {
				return nil, fmt.Errorf("Mismatch of the format string index: %d instead of %d", index, h.index)
			}
		} else {
			return nil, fmt.Errorf("Failed to read format string index err=%v", err)
		}
	}
	if ADD_SOURCE_LINE {
		// Read filename hash and source line number from the binary stream
		if filenameHash, err := readIntegerFromReader(reader, 2); err == nil {
			filename, ok := filenames[uint16(filenameHash)]
			if !ok {
				return nil, fmt.Errorf("Failed to find filename with hash %x", filenameHash)
			}
			logEntry.Filename = filename
		}
		if lineNumber, err := readIntegerFromReader(reader, 2); err == nil {
			logEntry.LineNumber = int(lineNumber)
		} else {
			return nil, fmt.Errorf("Failed to read source file linenumber err=%v", err)
		}
	}
	if SEND_LOG_INDEX {
		// Read log index - running counter of logs
		if logEntryIndex, err := readIntegerFromReader(reader, 8); err == nil {
			logEntry.Index = logEntryIndex
		} else {
			return nil, fmt.Errorf("Failed to read log index err=%v", err)
		}
	}
	if ADD_TIMESTAMP {
		// Read 64 bits of timestamp from the stream
		if timestamp, err := readIntegerFromReader(reader, 8); err == nil {
			logEntry.Timestamp = int64(timestamp)
		} else {
			return nil, fmt.Errorf("Failed to read timestamp err=%v", err)
		}
	}

	hFmtString := h.Args.fmtString
	args := make([]interface{}, 0)
	var value interface{}
	var err error
	// Read arguments from the binary stream
	for _, hArg := range h.Args.args {
		argType := hArg.decodeArg.argType
		if isIntegral(argType) {
			count := hArg.writer.getSize() // size of the integer I pushed into the binary stream
			value, err = readIntegerFromReader(reader, count)
		} else if hArg.decodeArg.argKind == reflect.String {
			value, err = readStringFromReader(reader)
		} else {
			return nil, fmt.Errorf("Can not handle type %v", argType)
		}
		if err == nil {
			args, err = appendArg(args, value, argType)
			if err != nil {
				return nil, fmt.Errorf("%v", err)
			}
		} else {
			return nil, fmt.Errorf("%v", err)
		}
	}
	logEntry.Args = args
	logEntry.FmtString = hFmtString
	return logEntry, nil
}

// GetIndexTable returns a map[hash]
// Application can use the map for decoding of the binary stread
// Pay attention that the map is getting updated every time a new string appears
func (b *Binlog) GetIndexTable() (map[uint32]*Handler, map[uint16]string) {
	return b.handlersLookupByHash, b.Filenames
}

func isIntegral(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return true
	case reflect.Int, reflect.Uint:
		return true
	default:
		return false
	}
}

func isUnsigned(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Uint, reflect.Uint32, reflect.Uint64, reflect.Uint8, reflect.Uint16:
		return true
	default:
		return false
	}
}

func readIntegerFromReader(reader io.Reader, count int) (uint64, error) {
	slice := make([]byte, count)
	n, err := reader.Read(slice)
	if (n > 0) && (n != count) {
		return 0, fmt.Errorf("Read %d bytes instead of %d, err=%v", n, count, err)
	} else if n == 0 {
		return 0, fmt.Errorf("EOF")
	}
	switch count {
	case 1:
		var value uint8
		binary.Read(bytes.NewBuffer(slice[:]), binary.LittleEndian, &value)
		return uint64(value), nil
	case 2:
		var value uint16
		binary.Read(bytes.NewBuffer(slice[:]), binary.LittleEndian, &value)
		return uint64(value), nil
	case 4:
		var value uint32
		binary.Read(bytes.NewBuffer(slice[:]), binary.LittleEndian, &value)
		return uint64(value), nil
	default:
		var value uint64
		binary.Read(bytes.NewBuffer(slice[:]), binary.LittleEndian, &value)
		return uint64(value), nil
	}
}

func readStringFromReader(reader io.Reader) (string, error) {
	// Read 2 bytes of the size of the string
	count := 2
	slice := make([]byte, count)
	n, err := reader.Read(slice)
	if (n > 0) && (n != count) {
		return "", fmt.Errorf("Read %d bytes instead of %d, err=%v", n, count, err)
	} else if n == 0 {
		return "", fmt.Errorf("EOF")
	}
	var value uint16
	binary.Read(bytes.NewBuffer(slice[:]), binary.LittleEndian, &value)
	count = int(value)
	slice = make([]byte, count)
	n, err = reader.Read(slice)
	if (n > 0) && (n != count) {
		return "", fmt.Errorf("Read %d bytes instead of %d, err=%v", n, count, err)
	} else if n == 0 {
		return "", fmt.Errorf("EOF")
	}
	return string(slice), nil
}

func appendArg(args []interface{}, arg interface{}, argType reflect.Type) ([]interface{}, error) {
	switch value := arg.(type) {
	case int:
		return append(args, int(value)), nil
	case uint:
		return append(args, uint(value)), nil
	case int8:
		return append(args, int8(value)), nil
	case int16:
		return append(args, int16(value)), nil
	case int32:
		return append(args, int32(value)), nil
	case int64:
		return append(args, int64(value)), nil
	case uint8:
		return append(args, uint8(value)), nil
	case uint16:
		return append(args, uint16(value)), nil
	case uint32:
		return append(args, uint32(value)), nil
	case uint64:
		return append(args, uint64(value)), nil
	case string:
		return append(args, string(value)), nil
	default:
		return nil, fmt.Errorf("Can not handle type %v", argType.Kind())
	}
}

func getStringAddress(s string) uint {
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(&s))
	return uint(sHeader.Data)
}

// Return index of the string given the string address
// Returning one integer shaves 10% off the overall vs return uint, error
// Golang inlines this function?
func (b *Binlog) getStringIndex(s string) uint {
	sDataOffset := (getStringAddress(s) - b.config.ConstDataBase) / ALIGNMENT
	if sDataOffset < b.config.ConstDataSize {
		b.statistics.StringOffsetOk++
		return sDataOffset
	} else {
		b.statistics.StringOOM++
		// fmt.Errorf("String %x is out of address range %x-%x", getStringAddress(s), b.config.ConstDataBase, b.config.ConstDataBase+b.config.ConstDataSize*ALIGNMENT)
		return b.config.ConstDataSize
	}
}

func md5sum(s string) uint32 {
	md5sum := md5.Sum([]byte(s))
	var hash uint32
	binary.Read(bytes.NewBuffer(md5sum[:]), binary.LittleEndian, &hash)
	return hash
}

func intToSlice(v interface{}) []byte {
	var bufHash bytes.Buffer
	binary.Write(&bufHash, binary.LittleEndian, v)
	return bufHash.Bytes()
}

func (b *Binlog) createHandler(fmtStr string, args []interface{}) (*Handler, error) {
	var h Handler
	h.Args.fmtString = fmtStr
	var err error
	h.Args.args, err = parseLogLine(fmtStr, args)
	if err != nil {
		return nil, err
	}

	if SEND_STRING_INDEX {
		index := atomic.AddUint32(&b.currentIndex, 1) // If I want the index to start from zero I can add (-1)
		var bufIndex bytes.Buffer
		binary.Write(&bufIndex, binary.LittleEndian, &index)
		h.index = bufIndex.Bytes()
		h.IndexUint = index
	}

	hash := md5sum(fmtStr)
	h.hash = intToSlice(&hash)
	h.HashUint = hash

	return &h, nil
}

// My hashtable is trivial: address of the string is an index in the array of handlers
// I assume that all strings are allocated in the same text section of the executable
// If this is not the case I try to use a map (8x slower)
// The end result of this function is a new handler for the fmtStr in L1 or L2 cache
func (b *Binlog) getHandler(fmtStr string, args []interface{}) (*Handler, error) {
	var h = &defaultHandler
	var err error
	var sIndex uint
	var isMiss = false
	sIndex = b.getStringIndex(fmtStr)
	if sIndex != b.config.ConstDataSize {
		h = b.L1Cache[sIndex]
		if h != nil { // fast cache hit? (20% of the whole function is here. Blame CPU data cache?)
			b.statistics.L1CacheHit++
		} else {
			isMiss = true
			b.statistics.L1CacheMiss++
			h, err = b.createHandler(fmtStr, args)
			if err != nil {
				log.Printf("%v", err)
				return nil, err
			}
			b.L1Cache[sIndex] = h
			b.handlersLookupByHash[h.HashUint] = h
		}
	} else {
		b.statistics.L2CacheUsed++
		// log.Printf("%v, use L2Cache instead", err)
		var ok bool
		if h, ok = b.L2Cache[fmtStr]; ok {
			b.statistics.L2CacheHit++
		} else {
			isMiss = true
			b.statistics.L2CacheMiss++
			h, err = b.createHandler(fmtStr, args)
			if err != nil {
				log.Printf("%v", err)
				return nil, err
			}
			b.L2Cache[fmtStr] = h
			b.handlersLookupByHash[h.HashUint] = h
		}
	}
	// Set filename and source line number
	if ADD_SOURCE_LINE && isMiss {
		var filenameHash uint16 = 0xBADB
		var fileLine uint16 = 0xADBA
		// Caller(0) is this function, Caller(1) is Log()
		_, filename, line, ok := runtime.Caller(2)
		if ok {
			filenameHash = uint16(md5sum(filename))
			fileLine = uint16(line)
		}
		h.FilenameHashUint = filenameHash
		h.LineNumberUint = fileLine
		h.filenameHash = intToSlice(&filenameHash)
		h.lineNumber = intToSlice(&fileLine)
		b.Filenames[h.FilenameHashUint] = filename
	}
	return h, nil
}

func (b *Binlog) writeArgumentToOutput_Slow(writer writer, arg interface{}) error {
	var err error
	rv := reflect.ValueOf(arg)
	var v uint64
	if k := rv.Kind(); k >= reflect.Int && k < reflect.Uint {
		v = uint64(rv.Int())
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&v))
	} else if k <= reflect.Uintptr {
		v = rv.Uint()
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&v))
	} else {
		return fmt.Errorf("Unsupported type: %T\n", reflect.TypeOf(arg))
	}
	/* write v */
	return err
}

func (b *Binlog) writeArgumentToOutput_Faster(writer writer, arg interface{}) error {
	// unsafe pointer to the data depends on the data type
	var err error
	switch arg := arg.(type) {
	case int:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case int8:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case int16:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case int32:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case int64:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case uint8:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case uint16:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case uint32:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case uint64:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	case uint:
		i := uint64(arg)
		err = writer.write(b.config.IOWriter, unsafe.Pointer(&i))
	default:
		return fmt.Errorf("Unsupported type: %T\n", reflect.TypeOf(arg))
	}
	return err
}

// According to https://golang.org/src/runtime/runtime2.go interface
// is a structure with two fields - type and reference to the data
type iface struct {
	tab  unsafe.Pointer // *itab
	data unsafe.Pointer
}

func getInterfaceData(arg interface{}) unsafe.Pointer {
	return unsafe.Pointer((((*iface)(unsafe.Pointer(&arg))).data))
}

// Cast the integer argument to uint64 and call a "writer"
// The "writer" knows how many bytes to add to the binary stream
//
// Type casts from interface{} to integer consume 40% of the overall
// time. Can I do better? What is interface{} in Golang?
// Switching to args *[]interface makes the performance 2x worse
// Before you jump to conclusions see
// https://groups.google.com/forum/#!topic/golang-nuts/Og8s9Y-Kif4
func (b *Binlog) writeArgumentToOutput(writer writer, arg interface{}) error {
	var err error
	// writer.write() expects an unsafe pointer
	// writer will copy the required number of bytes to the output binary stream
	err = writer.write(b.config.IOWriter, getInterfaceData(arg))
	return err
}

// Parse the format string, collect argument types, format verbs
// Should I call fmt package here?
func parseLogLine(gold string, args []interface{}) ([]*HandlerArg, error) {
	tmp := gold
	f := &tmp
	hArgs := make([]*HandlerArg, 0)
	var r rune
	var n int

	argIndex := 0
	for len(*f) > 0 {
		r, n = next(f)
		if r == utf8.RuneError && n == 0 {
			break
		}
		if r == utf8.RuneError {
			return nil, fmt.Errorf("Can not handle '%c' in %s: rune error", r, gold)
		}
		if r != '%' {
			continue
		}
		// Literal % sign
		if peek(f) == '%' {
			continue
		}
		r, _ = next(f)
		arg := args[argIndex]
		argType := reflect.TypeOf(arg)
		argKind := argType.Kind()
		count := int(argType.Size()) // number of bytes in the argument
		switch r {
		case 'x', 'd', 'i', 'c':
			writer := &writerByteArray{count: count}
			hArg := &HandlerArg{writer: writer, fmtVerb: r, decodeArg: DecodeArg{argType: argType, argKind: argKind}}
			hArgs = append(hArgs, hArg)
		case 's':
			writer := &writerString{}
			hArg := &HandlerArg{writer: writer, fmtVerb: r, decodeArg: DecodeArg{argType: argType, argKind: argKind}}
			hArgs = append(hArgs, hArg)
		default:
			return nil, fmt.Errorf("Can not handle '%c' in %s: unknown format code", r, gold)
		}
		argIndex++
	}

	return hArgs, nil
}

func peek(s *string) rune {
	r, _ := utf8.DecodeRuneInString(*s)

	return r
}

func next(s *string) (rune, int) {
	r, n := utf8.DecodeRuneInString(*s)
	*s = (*s)[n:]

	return r, n
}

func getTextAddressSize(maps []*maps.Maps) (constDataBase uint, constDataSize uint) {
	s := "TestString"
	sAddress := getStringAddress(s)
	for i := 0; i < len(maps); i++ {
		start := uint(maps[i].AddressStart)
		end := uint(maps[i].AddressEnd)
		if (sAddress >= start) && (sAddress <= end) {
			return start, end - start
		}
	}

	return 0, 0
}

// GetSelfTextAddressSize returns base address and size of the text segment
// There are two ways to do this - procfs or moduledata
func GetSelfTextAddressSize() (constDataBase uint, constDataSize uint) {
	selfPid := os.Getpid()
	process, err := procfs.NewProcess(selfPid, true)
	if err != nil {
		log.Fatalf("Fail to read procfs context %v", err)
	}
	maps, err := process.Maps()
	if err != nil {
		log.Fatalf("Fail to read procfs/maps context %v", err)
	}
	return getTextAddressSize(maps)

}

var defaultHandler Handler

type writer interface {
	// I need a sufficiently abstract API which does not involve
	// interface{} and still can accept pointers to arbitrary objects
	// In C I would use (void*)
	write(io.Writer, unsafe.Pointer) error

	getSize() int
}

type writerByteArray struct {
	count int
}

func (w *writerByteArray) getSize() int {
	return w.count
}

// Copy w.count bytes from the unsafe pointer to the byte stream
func (w *writerByteArray) write(ioWriter io.Writer, data unsafe.Pointer) error {
	// I am doing something which https://golang.org/pkg/unsafe/ explicitly forbids
	var hdr reflect.SliceHeader
	hdr.Len = w.count
	hdr.Data = uintptr(unsafe.Pointer((*byte)(data)))
	hdr.Cap = w.count

	dataToWrite := *((*[]byte)(unsafe.Pointer(&hdr)))
	// In the benchmarks this callback is an empty function
	_, err := ioWriter.Write(dataToWrite)
	return err
}

type writerString struct {
}

func (w *writerString) getSize() int {
	return 0
}

// Write 16 bits length of the string followed by the string itself
// TODO: if the string is const I need only it's hash
func (w *writerString) write(ioWriter io.Writer, data unsafe.Pointer) error {
	// I am doing something which https://golang.org/pkg/unsafe/ explicitly forbids
	var hdr = (*reflect.StringHeader)(data)
	writer := &writerByteArray{2}
	if err := writer.write(ioWriter, unsafe.Pointer(&(hdr.Len))); err != nil {
		return err
	}

	writer = &writerByteArray{hdr.Len}
	if err := writer.write(ioWriter, unsafe.Pointer(hdr.Data)); err != nil {
		return err
	}

	return nil
}
