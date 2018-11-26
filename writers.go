package binlog

import (
	"io"
	"reflect"
	"unsafe"
)

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
func (w *writerString) write(ioWriter io.Writer, data unsafe.Pointer) error {
	// I am doing something which https://golang.org/pkg/unsafe/ explicitly forbids
	var hdr *reflect.StringHeader = (*reflect.StringHeader)(data)
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
