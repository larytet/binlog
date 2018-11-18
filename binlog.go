package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	"log"
	"reflect"
	"unsafe"
	//"runtime"
	//	"os"
)

type handler struct {
	formatString string
}

var defaultHandler handler

type Binlog struct {
	constDataBase uint
	constDataSize uint
	handlers      []*handler
}

// constDataBase is an address of the initialzied const data, constDataSize is it's size
func Init(constDataBase uint, constDataSize uint) *Binlog {
	// allocate one handler more for handling default cases
	binlog := &Binlog{constDataBase: constDataBase, constDataSize: constDataSize, handlers: make([]*handler, constDataSize+1)}
	return binlog
}

func (b *Binlog) getStringIndex(s string) uint {
	sHeader := (*reflect.StringHeader)(unsafe.Pointer(&s))
	sData := sHeader.Data
	sDataOffset := uint(sData) - b.constDataBase
	if sDataOffset < b.constDataSize {
		return sDataOffset / 8
	} else {
		log.Printf("String %x is out of address range %x-%x", sHeader.Data, b.constDataBase, b.constDataBase+b.constDataSize)
		return b.constDataSize
	}

}

func (b *Binlog) addHandler(s string) {
	var h handler
	h.formatString = s
}

// All arguments are uint32
func (b *Binlog) PrintUint32(s string, args ...uint32) {
	var h *handler = &defaultHandler
	sIndex := b.getStringIndex(s)
	if sIndex != b.constDataSize {
		h = b.handlers[sIndex]
		if b.handlers[sIndex] == nil { // cache miss?
			b.addHandler(s)
			h = b.handlers[sIndex]
		}
	}
}
