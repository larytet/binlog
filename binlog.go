package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"github.com/jandre/procfs"
	"github.com/jandre/procfs/maps"
	"io"
	"log"
	"os"
	"reflect"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

/*
#cgo CFLAGS: -std=c99

#include <stdlib.h>
#include <stdint.h>
*/
import "C"

type handlerArg struct {
	writer  writer
	fmtCode rune         // for example, x (from %x)
	argType reflect.Type // type of the argument, for example int32
}

type handler struct {
	fmtString string        // the format string itself for decoding
	args      []*handlerArg // list of functions to output the data correctly 1,4 or 8 bytes of integer
	hashUint  uint32        // hash of the format string
	indexUint uint32        // hash of the format string

	// I can output only byte slices, therefore I keep slices
	index []byte // a running index of the handler
	hash  []byte // hash of the format string
}

var defaultHandler handler

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

type Binlog struct {
	constDataBase uint
	constDataSize uint
	currentIndex  uint32
	ioWriter      io.Writer

	// Index in this array is a virtual address of the format string
	// This is for fast lookup of constant strings from the executable
	// code section
	handlersArray []*handler

	// Index in this array is the string itself
	// I need this map for lookup of string which address is not
	// part of the executable code section
	handlersMap map[string]*handler

	// This is map[format string hash]*handler
	// I need this map for decoding of the binary stream
	handlersMapHash map[uint32]*handler
}

const ALIGNMENT uint = 8

// constDataBase is an address of the initialzied const data, constDataSize is it's size
func Init(ioWriter io.Writer, constDataBase uint, constDataSize uint) *Binlog {
	// allocate one handler more for handling default cases
	constDataSize = constDataSize / ALIGNMENT
	handlersArray := make([]*handler, constDataSize+1)
	handlersMap := make(map[string]*handler)
	handlersMapHash := make(map[uint32]*handler)
	binlog := &Binlog{
		constDataBase:   constDataBase,
		constDataSize:   constDataSize,
		handlersArray:   handlersArray,
		handlersMap:     handlersMap,
		handlersMapHash: handlersMapHash,
		ioWriter:        ioWriter}
	return binlog
}

func getStringAdress(s string) uintptr {
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(&s))
	return sHeader.Data
}

func (b *Binlog) getStringIndex(s string) (uint, error) {
	sData := getStringAdress(s)
	sDataOffset := (uint(sData) - b.constDataBase) / ALIGNMENT
	if sDataOffset < b.constDataSize {
		return sDataOffset, nil
	} else {
		return b.constDataSize, fmt.Errorf("String %x is out of address range %x-%x", sData, b.constDataBase, b.constDataBase+b.constDataSize*ALIGNMENT)
	}
}

func (b *Binlog) createHandler(fmtStr string, args []interface{}) (*handler, error) {
	var h handler
	h.fmtString = fmtStr
	var err error
	h.args, err = parseLogLine(fmtStr, args)
	if err != nil {
		return nil, err
	}

	index := atomic.AddUint32(&b.currentIndex, 1) // If I want the index to start from zero I can add (-1)
	var bufIndex bytes.Buffer
	binary.Write(&bufIndex, binary.LittleEndian, &index)
	h.index = bufIndex.Bytes()

	md5sum := md5.Sum([]byte(fmtStr))
	var hash uint32
	binary.Read(bytes.NewBuffer(md5sum[:]), binary.LittleEndian, &hash)
	var bufHash bytes.Buffer
	binary.Write(&bufHash, binary.LittleEndian, &hash)
	h.hash = bufHash.Bytes()
	h.hashUint = hash
	h.indexUint = index

	return &h, nil
}

// My hashtable is trivial: address of the string is an index in the array of handlers
// I assume that all strings are allocated in the same text section of the executable
// If this is not the case I try to use a map (slower)
func (b *Binlog) getHandler(fmtStr string, args []interface{}) (*handler, error) {
	var h *handler = &defaultHandler
	var err error
	var sIndex uint
	sIndex, _ = b.getStringIndex(fmtStr)
	if sIndex != b.constDataSize {
		h = b.handlersArray[sIndex]
		if h == nil { // hashtable miss?
			h, err = b.createHandler(fmtStr, args)
			if err != nil {
				log.Printf("%v", err)
				return nil, err
			}
			b.handlersArray[sIndex] = h
			b.handlersMapHash[h.hashUint] = h
		}
	} else {
		var ok bool
		if h, ok = b.handlersMap[fmtStr]; !ok {
			h, err = b.createHandler(fmtStr, args)
			if err != nil {
				log.Printf("%v", err)
				return nil, err
			}
			b.handlersMap[fmtStr] = h
			b.handlersMapHash[h.hashUint] = h
		}
	}
	return h, nil
}

func (b *Binlog) writeIntegerToWriter(writer writer, arg interface{}) error {
	// unsafe pointer to the data depends on the data type
	var err error
	switch arg.(type) {
	case int8:
		i := uint64(arg.(int8))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case int16:
		i := uint64(arg.(int16))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case int32:
		i := uint64(arg.(int32))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case int64:
		i := uint64(arg.(int64))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case uint8:
		i := uint64(arg.(uint8))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case uint16:
		i := uint64(arg.(uint16))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case uint32:
		i := uint64(arg.(uint32))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case uint64:
		i := uint64(arg.(uint64))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	case int:
		i := uint64(arg.(int))
		err = writer.write(b.ioWriter, unsafe.Pointer(&i))
	default:
		return fmt.Errorf("Unsupported type: %T\n", reflect.TypeOf(arg))
	}
	return err
}

// similar to fmt.Printf()
func (b *Binlog) Log(fmtStr string, args ...interface{}) error {
	h, err := b.getHandler(fmtStr, args)
	if err != nil {
		return err
	}
	hArgs := h.args
	if len(hArgs) != len(args) {
		return fmt.Errorf("Number of args %d does not match log line %d", len(args), len(hArgs))
	}
	b.ioWriter.Write(h.hash)
	b.ioWriter.Write(h.index)
	for i, arg := range args {
		hArg := h.args[i]
		writer := hArg.writer
		if err := b.writeIntegerToWriter(writer, arg); err != nil {
			return fmt.Errorf("Failed to write value %v", err)
		}

	}
	return nil
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
		return 0, fmt.Errorf("%v", err)
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

// Recover the human readable log from the binary stream
func (b *Binlog) Print(reader io.Reader) (bytes.Buffer, error) {
	var out bytes.Buffer
	for {
		// Read format string hash
		var h *handler
		if hashUint, err := readIntegerFromReader(reader, 4); err == nil {
			var ok bool
			if h, ok = b.handlersMapHash[uint32(hashUint)]; !ok {
				return out, fmt.Errorf("Failed to find format string hash %x", hashUint)
			}
		} else if err.Error() == "EOF" {
			return out, nil
		} else {
			return out, fmt.Errorf("Failed to read format string hash err=%v", err)
		}

		// Read format string index
		if index, err := readIntegerFromReader(reader, 4); err == nil {
			if uint32(index) != h.indexUint {
				return out, fmt.Errorf("Mismatch of the format string index: %d instead of %d", index, h.index)
			}
		} else {
			return out, fmt.Errorf("Failed to read format string index err=%v", err)
		}

		hFmtString := h.fmtString
		args := make([]interface{}, 0)
		// Read arguments from the binary stream
		for _, hArg := range h.args {
			argType := hArg.argType
			if isIntegral(argType) { // integer is always 64 bits
				if value, err := readIntegerFromReader(reader, 8); err == nil {
					args, err = appendArg(args, value, argType)
					if err != nil {
						return out, fmt.Errorf("Failed to read 64 bits err=%v", err)
					}
				} else {
					return out, fmt.Errorf("Failed to read 64 bits err=%v", err)
				}
			} else {
				return out, fmt.Errorf("Can not handle type %v", argType)
			}
		}
		// format and push the log to the user output buffer
		s := fmt.Sprintf(hFmtString, args...)
		out.WriteString(s)
	}
	return out, nil
}

func parseLogLine(gold string, args []interface{}) ([]*handlerArg, error) {
	tmp := gold
	f := &tmp
	hArgs := make([]*handlerArg, 0)
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
		switch r {
		case 'x', 'd', 'i', 'c':
			writer := &writerByteArray{count: int(argType.Size())}
			hArg := &handlerArg{writer: writer, argType: argType, fmtCode: r}
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
	sAddress := uint(getStringAdress(s))
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
