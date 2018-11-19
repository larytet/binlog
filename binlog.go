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

type handler struct {
	fmtString string   // the format string itself for decoding
	writers   []writer // list of functions to output the data correctly 1,4 or 8 bytes of integer
	segs      []rune   // list of format codes
	hashUint  uint32   // hash of the format string
	indexUint uint32   // hash of the format string

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

	// Index in this array is the hash of format string
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

func (b *Binlog) createHandler(fmtStr string, args ...interface{}) (*handler, error) {
	var h handler
	h.fmtString = fmtStr
	var err error
	h.writers, h.segs, err = parseLogLine(fmtStr, args)
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
func (b *Binlog) getHandler(fmtStr string, args ...interface{}) (*handler, error) {
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
		}
	}
	b.handlersMapHash[h.hashUint] = h
	return h, nil
}

// similar to fmt.Printf()
func (b *Binlog) Log(fmtStr string, args ...interface{}) error {
	h, err := b.getHandler(fmtStr, args)
	if err != nil {
		return err
	}
	writers := h.writers
	if len(writers) != len(args) {
		return fmt.Errorf("Number of args %d does not match log line %d", len(args), len(writers))
	}
	b.ioWriter.Write(h.hash)
	b.ioWriter.Write(h.index)
	for i, arg := range args {
		// "writer" depends on the format code
		writer := writers[i]
		// unsafe pointer to the data depends on the data type
		switch t := arg.(type) {
		case int32:
			i := int64(arg.(int32))
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		case int64:
			i := arg.(int64)
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		case uint64:
			i := arg.(uint64)
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		case int:
			i := int64(arg.(int))
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		default:
			return fmt.Errorf("Unsupported type: %T\n", t)
		}
	}
	return nil
}

// Recover the human readable log from the binary stream
func (b *Binlog) Print(reader io.Reader) (bytes.Buffer, error) {
	var out bytes.Buffer
	for {
		hash := make([]byte, 4)
		// Read format string hash
		n, err := reader.Read(hash)
		if n < 4 {
			if n == 0 {
				return out, nil
			} else {
				return out, fmt.Errorf("Failed to read format string hash n=%d err=%v", n, err)
			}
		}
		var hashUint uint32
		binary.Read(bytes.NewBuffer(hash[:]), binary.LittleEndian, &hashUint)
		var h *handler
		var ok bool
		if h, ok = b.handlersMapHash[hashUint]; !ok {
			return out, fmt.Errorf("Failed to find format string hash %x", hashUint)
		}

		// Read format string index
		n, err = reader.Read(hash)
		if n < 4 {
			return out, fmt.Errorf("Failed to read format string index %v", err)
		}
		var index uint32
		binary.Read(bytes.NewBuffer(hash[:]), binary.LittleEndian, &index)
		if index != h.indexUint {
			return out, fmt.Errorf("Mismatch of the format index %d instead of %d", index, h.index)
		}

		args := make([]interface{}, 0)
		for i, w := range h.writers {
			count := w.getSize()
			seg := h.segs[i]

			switch count {
			case 1:
				buf := make([]byte, 1)
				n, _ := reader.Read(buf)
				if n < 1 {
					return out, fmt.Errorf("Failed to read 1 byte integer, got %d", n)
				}
				if seg == 'x' {
					var arg uint8
					binary.Read(bytes.NewBuffer(buf), binary.LittleEndian, &arg)
					args = append(args, arg)
				} else {
					var arg uint8
					binary.Read(bytes.NewBuffer(buf), binary.LittleEndian, &arg)
					args = append(args, arg)
				}
			case 8:
				buf := make([]byte, 8)
				n, _ := reader.Read(buf)
				if n < 8 {
					return out, fmt.Errorf("Failed to read 8 bytes integer, got %d", n)
				}
				if seg == 'x' {
					var arg uint64
					binary.Read(bytes.NewBuffer(buf), binary.LittleEndian, &arg)
					args = append(args, arg)
				} else {
					var arg uint64
					binary.Read(bytes.NewBuffer(buf), binary.LittleEndian, &arg)
					args = append(args, arg)
				}
			default:
				return out, fmt.Errorf("Can not handle arguments with size %d", count)
			}

		}
		fmtString := h.fmtString
		out.WriteString(fmt.Sprintf(fmtString, args...))
	}
	return out, nil
}

func parseLogLine(gold string, args ...interface{}) ([]writer, []rune, error) {
	tmp := gold
	f := &tmp
	writers := make([]writer, 0)
	segs := make([]rune, 0)
	var r rune
	var n int

	for len(*f) > 0 {
		r, n = next(f)
		if r == utf8.RuneError && n == 0 {
			break
		}
		if r == utf8.RuneError {
			return nil, nil, fmt.Errorf("Can not handle '%c' in %s: rune error", r, gold)
		}
		if r != '%' {
			continue
		}
		// Literal % sign
		if peek(f) == '%' {
			continue
		}
		r, _ = next(f)

		switch r {
		case 'x':
			writers = append(writers, &writerByteArray{count: 8})
		case 'd':
			writers = append(writers, &writerByteArray{count: 8})
		case 'i':
			writers = append(writers, &writerByteArray{count: 8})
		case 'c':
			writers = append(writers, &writerByteArray{count: 1})
		default:
			return nil, nil, fmt.Errorf("Can not handle '%c' in %s: unknown format code", r, gold)
		}
		segs = append(segs, r)
	}

	return writers, segs, nil
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
