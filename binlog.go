package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"github.com/jandre/procfs"
	"github.com/jandre/procfs/maps"
	"github.com/larytet-go/moduledata"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

// I keep hash of the format string and index of the string in the
// cache. When I decode the binary stream I can ensure that both 32 bits hash
// and index of the string match. This flag is useful for debug or fast lookup
// when decoding binary streams
var SEND_STRING_INDEX bool = false

// Add hash of the filename (16 bits) and line in the source (16 bits)
// to the binary stream
// I assume the the goloang does not dedups the constant strings and all
// calls to the Log() use unique string. There is a test which ensures this
var ADD_SOURCE_LINE bool = false

type HandlerArg struct {
	writer  writer
	FmtCode rune         // for example, x (from %x)
	ArgType reflect.Type // type of the argument, for example int32
	ArgKind reflect.Kind // type of the argument, for example int32
}

type Handler struct {
	FmtString        string        // the format string itself for decoding
	Address          uintptr       // address of the string
	IsL1Cache        bool          // true if the string in the L1 cache
	Args             []*HandlerArg // list of functions to output the data correctly 1,4 or 8 bytes of integer
	HashUint         uint32        // hash of the format string
	IndexUint        uint32        // hash of the format string
	FilenameHashUint uint16        // hash of the filename
	LineNumberUint   uint16        // source line number

	// I can output only byte slices, therefore I keep slices
	index        []byte // a running index of the handler
	hash         []byte // hash of the format string
	filenameHash []byte
	lineNumber   []byte
}

type Statistics struct {
	L1CacheMiss uint64
	L2CacheMiss uint64
	L1CacheHit  uint64
	L2CacheHit  uint64
	L2CacheUsed uint64
}

type Binlog struct {
	constDataBase uint
	constDataSize uint
	currentIndex  uint32
	ioWriter      io.Writer

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

const ALIGNMENT uint = 8

// constDataBase is an address of the initialzied const data, constDataSize is it's size
func Init(ioWriter io.Writer, constDataBase uint, constDataSize uint) *Binlog {
	// allocate one handler more for handling default cases
	constDataSize = constDataSize / ALIGNMENT
	L1Cache := make([]*Handler, constDataSize+1)
	L2Cache := make(map[string]*Handler)
	filenames := make(map[uint16]string)
	handlersLookupByHash := make(map[uint32]*Handler)
	binlog := &Binlog{
		constDataBase:        constDataBase,
		constDataSize:        constDataSize,
		L1Cache:              L1Cache,
		L2Cache:              L2Cache,
		Filenames:            filenames,
		handlersLookupByHash: handlersLookupByHash,
		ioWriter:             ioWriter}
	return binlog
}

func (b *Binlog) GetStatistics() Statistics {
	return b.statistics
}

// similar to b.ioWriter.Write([]byte(fmt.Printf(fmtStr, args)))
func (b *Binlog) Log(fmtStr string, args ...interface{}) error {
	h, err := b.getHandler(fmtStr, args)
	if err != nil {
		return err
	}

	hArgs := h.Args
	if len(hArgs) != len(args) {
		return fmt.Errorf("Number of args %d does not match log line %d", len(args), len(hArgs))
	}
	b.ioWriter.Write(h.hash)

	if SEND_STRING_INDEX {
		b.ioWriter.Write(h.index)
	}

	if ADD_SOURCE_LINE {
		b.ioWriter.Write(h.filenameHash)
		b.ioWriter.Write(h.lineNumber)
	}

	for i, arg := range args {
		hArg := h.Args[i]
		writer := hArg.writer
		argKind := hArg.ArgKind
		if err := b.writeArgumentToOutput(writer, arg, argKind); err != nil {
			return fmt.Errorf("Failed to write value %v", err)
		}
	}
	return nil
}

type LogEntry struct {
	filename   string
	lineNumber int
	fmtString  string
	args       []interface{}
}

// Recover the human readable log from the binary stream
func (b *Binlog) DecodeNext(reader io.Reader) (*LogEntry, error) {
	indexTable, filenames := b.GetIndexTable()
	return DecodeNext(reader, indexTable, filenames)
}

// Recover the human readable log from the binary stream
func DecodeNext(reader io.Reader, indexTable map[uint32]*Handler, filenames map[uint16]string) (*LogEntry, error) {
	var logEntry *LogEntry = &LogEntry{}
	// Read format string hash
	var h *Handler
	if hashUint, err := readIntegerFromReader(reader, 4); err == nil {
		var ok bool
		if h, ok = indexTable[uint32(hashUint)]; !ok {
			return nil, fmt.Errorf("Failed to find format string hash %x", hashUint)
		}
	} else {
		return nil, err
	}

	// Read format string index
	if SEND_STRING_INDEX {
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
			logEntry.filename = filename
		}
		if lineNumber, err := readIntegerFromReader(reader, 2); err == nil {
			logEntry.lineNumber = int(lineNumber)
		} else {
			return nil, fmt.Errorf("Failed to read source file linenumber err=%v", err)
		}
	}

	hFmtString := h.FmtString
	args := make([]interface{}, 0)
	// Read arguments from the binary stream
	for _, hArg := range h.Args {
		argType := hArg.ArgType
		count := hArg.writer.getSize() // size of the integer I pushed into the binary stream
		if isIntegral(argType) {
			value, err := readIntegerFromReader(reader, count)
			if err == nil {
				args, err = appendArg(args, value, argType)
				if err != nil {
					return nil, fmt.Errorf("%v", err)
				}
			} else {
				return nil, fmt.Errorf("%v", err)
			}
		} else {
			return nil, fmt.Errorf("Can not handle type %v", argType)
		}
	}
	logEntry.args = args
	logEntry.fmtString = hFmtString
	return logEntry, nil
}

// Returns a map[hash]
// Application can use the map for decoding of the binary stread
// Pay attention that the map is getting updated every time a new string appears
func (b *Binlog) GetIndexTable() (map[uint32]*Handler, map[uint16]string) {
	return b.handlersLookupByHash, b.Filenames
}

type astVisitor struct {
	fmtStrings []string
}

func (v *astVisitor) Init() {
	v.fmtStrings = make([]string, 0)
}

func (v astVisitor) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		return nil
	}
	var packageName string
	var functionName string
	var args []ast.Expr
	var fmtString string
	switch astCallExpr := n.(type) {
	case *ast.CallExpr:
		switch astSelectExpr := astCallExpr.Fun.(type) {
		case *ast.SelectorExpr:
			switch astSelectExprX := astSelectExpr.X.(type) {
			case *ast.Ident:
				packageName = astSelectExprX.Name
			}
			astSelectExprSel := astSelectExpr.Sel
			functionName = astSelectExprSel.Name
		}
		args = astCallExpr.Args
	}
	if (packageName != "binlog") || (functionName != "Log") {
		return v
	}
	if len(args) < 1 {
		return v
	}
	switch arg0 := (args[0]).(type) {
	case *ast.BasicLit:
		fmtString = arg0.Value
		v.fmtStrings = append(v.fmtStrings, fmtString)
		//log.Printf("%v", v.fmtStrings)
	}
	return v
}

func collectBinlogArguments(astFile *ast.File) (*astVisitor, error) {
	//decls := astFile.Decls
	var v astVisitor
	(&v).Init()
	ast.Walk(v, astFile)
	return &v, nil
}

// This function is a work in progress, requires walking the Go AST
//
// Depends on debug/elf package, go/parse and go/ast packages
// Given an executable and the source files returns index tables required for decoding
// of the binary logs
// GetIndexTable() parses the ELF file, reads paths of the modules from the executable,
// parses the sources, finds all calls to binlog.Log(), generates hashes of the format
// strings, list of arguments
// See also http://goast.yuroyoro.net/
// https://stackoverflow.com/questions/46115312/use-ast-to-get-all-function-calls-in-a-function
func GetIndexTable(filename string) (map[uint32]*Handler, map[uint16]string, error) {
	allModules, err := moduledata.GetModules(filename)
	if err != nil {
		return nil, nil, err
	}
	goModules := make([]string, 0)
	for _, module := range allModules {
		if strings.HasSuffix(module, ".go") {
			goModules = append(goModules, module)
		}
	}
	skipped := 0
	log.Printf("Going to process %d Go modules in the %s", len(goModules), filename)
	for _, module := range goModules {
		fset := token.NewFileSet()
		astFile, err := parser.ParseFile(fset, module, nil, 0)
		if err != nil {
			log.Printf("Skipping %s, %v", module, err)
			skipped++
			continue
		}
		astVisitor, err := collectBinlogArguments(astFile)
		foundFmtStrings := len(astVisitor.fmtStrings)
		if foundFmtStrings > 0 {
			log.Printf("Found %d matches", foundFmtStrings)
		}
	}
	if skipped != 0 {
		log.Printf("Skipped %d modules", skipped)
	}

	return nil, nil, nil
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

func appendArg(args []interface{}, value uint64, argType reflect.Type) ([]interface{}, error) {
	switch argType.Kind() {
	case reflect.Int:
		return append(args, int(value)), nil
	case reflect.Uint:
		return append(args, uint(value)), nil
	case reflect.Int8:
		return append(args, int8(value)), nil
	case reflect.Int16:
		return append(args, int16(value)), nil
	case reflect.Int32:
		return append(args, int32(value)), nil
	case reflect.Int64:
		return append(args, int64(value)), nil
	case reflect.Uint8:
		return append(args, uint8(value)), nil
	case reflect.Uint16:
		return append(args, uint16(value)), nil
	case reflect.Uint32:
		return append(args, uint32(value)), nil
	case reflect.Uint64:
		return append(args, uint64(value)), nil
	default:
		return nil, fmt.Errorf("Can not handle type %v", argType.Kind())
	}
}

func getStringAddress(s string) uint {
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(&s))
	return uint(sHeader.Data)
}

// Return index of the string given the string address
func (b *Binlog) getStringIndex(s string) (uint, error) {
	sDataOffset := (getStringAddress(s) - b.constDataBase) / ALIGNMENT
	if sDataOffset < b.constDataSize {
		return sDataOffset, nil
	} else {
		return b.constDataSize, fmt.Errorf("String %x is out of address range %x-%x", getStringAddress(s), b.constDataBase, b.constDataBase+b.constDataSize*ALIGNMENT)
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
	h.FmtString = fmtStr
	var err error
	h.Args, err = parseLogLine(fmtStr, args)
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
// If this is not the case I try to use a map (slower)
// The end result of this function is a new handler for the fmtStr in L1 or L2 cache
func (b *Binlog) getHandler(fmtStr string, args []interface{}) (*Handler, error) {
	var h *Handler = &defaultHandler
	var err error
	var sIndex uint
	var isMiss bool = false
	sIndex, _ = b.getStringIndex(fmtStr)
	if sIndex != b.constDataSize {
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

// Cast the integer argument to uint64 and call a "writer"
// The "writer" knows how many bytes to add to the binary stream
func (b *Binlog) writeArgumentToOutput(writer writer, arg interface{}, argKind reflect.Kind) error {
	// unsafe pointer to the data depends on the data type
	var err error
	switch argKind {
	case reflect.Int8:
		i := uint64(arg.(int8))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Int16:
		i := uint64(arg.(int16))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Int32:
		i := uint64(arg.(int32))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Int64:
		i := uint64(arg.(int64))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Uint8:
		i := uint64(arg.(uint8))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Uint16:
		i := uint64(arg.(uint16))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Uint32:
		i := uint64(arg.(uint32))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Uint64:
		i := uint64(arg.(uint64))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Int:
		i := uint64(arg.(int))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case reflect.Uint:
		i := uint64(arg.(uint))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	default:
		return fmt.Errorf("Unsupported type: %T\n", reflect.TypeOf(arg))
	}
	return err
}

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
			hArg := &HandlerArg{writer: writer, ArgType: argType, FmtCode: r, ArgKind: argKind}
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

func SprintfMaps(maps []*maps.Maps) string {
	s := ""
	for _, m := range maps {
		s = s + fmt.Sprintf("\n%v", (*m))
	}
	return s
}

var defaultHandler Handler

type writer interface {
	write(io.Writer, unsafe.Pointer) error
	getSize() int
}

type writerByteArray struct {
	count int
}

func (w *writerByteArray) getSize() int {
	return w.count
}

func (w *writerByteArray) write(ioWriter io.Writer, data unsafe.Pointer) error {
	// I am doing something which https://golang.org/pkg/unsafe/ explicitly forbids
	var hdr reflect.SliceHeader
	hdr.Len = w.count
	hdr.Data = uintptr(unsafe.Pointer((*byte)(data)))
	hdr.Cap = w.count

	dataToWrite := *((*[]byte)(unsafe.Pointer(&hdr)))
	//log.Printf("Writing %v, count=%d", dataToWrite, w.count)
	_, err := ioWriter.Write(dataToWrite)
	return err
}
