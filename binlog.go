package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
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
	formatString string
	index        uint32
	writers      []writer
}

var defaultHandler handler

type writer interface {
	write(io.Writer, unsafe.Pointer) error
}

type writerByteArray struct {
	count int
}

func (w *writerByteArray) write(ioWriter io.Writer, data unsafe.Pointer) error {
	// I am doing something which https://golang.org/pkg/unsafe/ implicitly forbids
	var hdr reflect.SliceHeader
	hdr.Len = w.count
	hdr.Data = uintptr(unsafe.Pointer((*byte)(data)))
	hdr.Cap = w.count
	_, err := ioWriter.Write(*((*[]byte)(unsafe.Pointer(&hdr))))
	return err
}

type Binlog struct {
	constDataBase uint
	constDataSize uint
	handlers      []*handler
	currentIndex  uint32
	ioWriter      io.Writer
}

const ALIGNMENT uint = 8

// constDataBase is an address of the initialzied const data, constDataSize is it's size
func Init(ioWriter io.Writer, constDataBase uint, constDataSize uint) *Binlog {
	// allocate one handler more for handling default cases
	constDataSize = constDataSize / ALIGNMENT
	handlers := make([]*handler, constDataSize+1)
	binlog := &Binlog{constDataBase: constDataBase, constDataSize: constDataSize, handlers: handlers, ioWriter: ioWriter}
	return binlog
}

func getStringAdress(s string) uintptr {
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(&s))
	return sHeader.Data
}

func (b *Binlog) getStringIndex(s string) uint {
	sData := getStringAdress(s)
	sDataOffset := (uint(sData) - b.constDataBase) / ALIGNMENT
	if sDataOffset < b.constDataSize {
		return sDataOffset
	} else {
		log.Printf("String %x is out of address range %x-%x", sData, b.constDataBase, b.constDataBase+b.constDataSize*ALIGNMENT)
		return b.constDataSize
	}

}

func (b *Binlog) createHandler(fmt string) (*handler, error) {
	var h handler
	h.index = atomic.AddUint32(&b.currentIndex, 1) // If I want to start from zero I can add (-1)
	h.formatString = fmt
	var err error
	h.writers, err = parseLogLine(fmt)
	return &h, err
}

// similar to fmt.Printf()
func (b *Binlog) Log(fmtStr string, args ...interface{}) error {
	var err error
	var h *handler = &defaultHandler
	sIndex := b.getStringIndex(fmtStr)
	if sIndex != b.constDataSize {
		h = b.handlers[sIndex]
		if h == nil { // cache miss?
			h, err = b.createHandler(fmtStr)
			if err != nil {
				log.Printf("%v", err)
				return err
			}
			b.handlers[sIndex] = h
		}
	}
	writers := h.writers
	if len(writers) != len(args) {
		return fmt.Errorf("Number of args %d does not match log line %d", len(args), len(writers))
	}
	for i, arg := range args {
		// "writer" depends on the format code
		writer := writers[i]
		// unsafe pointer to the data depends on the data type
		switch t := arg.(type) {
		case int32:
			i := arg.(int32)
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		case int64:
			i := arg.(int64)
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		case int:
			i := arg.(int)
			writer.write(b.ioWriter, unsafe.Pointer(&i))
		default:
			return fmt.Errorf("Unsupported type: %T\n", t)
		}

	}
	return nil
}

func parseLogLine(gold string) ([]writer, error) {
	tmp := gold
	f := &tmp
	writers := make([]writer, 0)
	var r rune
	var n int

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

		switch r {
		case 'x':
			writers = append(writers, &writerByteArray{count: 8})
		case 'd':
			writers = append(writers, &writerByteArray{count: 8})
		case 'i':
			writers = append(writers, &writerByteArray{count: 8})
		case 'u':
			writers = append(writers, &writerByteArray{count: 8})
		case 'c':
			writers = append(writers, &writerByteArray{count: 1})
		default:
			return nil, fmt.Errorf("Can not handle '%c' in %s: unknown format code", r, gold)
		}

	}

	return writers, nil
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
