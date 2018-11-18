package binlog

// Based on the idea https://github.com/ScottMansfield/nanolog/issues/4
import (
	"atomic"
	"fmt"
	"log"
	"reflect"
	"unicode/utf8"
	"unsafe"
)

// Logger is the internal struct representing the runtime state of the loggers.
// The Segs field is not used during logging; it is only used in the inflate
// utility
type Logger struct {
	Kinds []reflect.Kind
	Segs  []string
}

// the following is from https://github.com/larytet/procfs
func parseLogLine(gold string) (Logger, []string) {
	// make a copy we can destroy
	tmp := gold
	f := &tmp
	var kinds []reflect.Kind
	var segs []string
	var curseg []rune

	for len(*f) > 0 {
		if r := next(f); r != '%' {
			curseg = append(curseg, r)
			continue
		}

		// Literal % sign
		if peek(f) == '%' {
			next(f)
			curseg = append(curseg, '%')
			continue
		}

		segs = append(segs, string(curseg))
		curseg = curseg[:0]

		var requireBrace bool

		// Optional curly braces around format
		r := next(f)
		if r == '{' {
			requireBrace = true
			r = next(f)
		}

		// optimized parse tree
		switch r {
		case 'b':
			kinds = append(kinds, reflect.Bool)

		case 's':
			kinds = append(kinds, reflect.String)

		case 'i':
			if len(*f) == 0 {
				kinds = append(kinds, reflect.Int)
				break
			}

			r := peek(f)
			switch r {
			case '8':
				next(f)
				kinds = append(kinds, reflect.Int8)

			case '1':
				next(f)
				if next(f) != '6' {
					logpanic("Was expecting i16.", gold)
				}
				kinds = append(kinds, reflect.Int16)

			case '3':
				next(f)
				if next(f) != '2' {
					logpanic("Was expecting i32.", gold)
				}
				kinds = append(kinds, reflect.Int32)

			case '6':
				next(f)
				if next(f) != '4' {
					logpanic("Was expecting i64.", gold)
				}
				kinds = append(kinds, reflect.Int64)

			default:
				kinds = append(kinds, reflect.Int)
			}

		case 'u':
			if len(*f) == 0 {
				kinds = append(kinds, reflect.Uint)
				break
			}

			r := peek(f)
			switch r {
			case '8':
				next(f)
				kinds = append(kinds, reflect.Uint8)

			case '1':
				next(f)
				if next(f) != '6' {
					logpanic("Was expecting u16.", gold)
				}
				kinds = append(kinds, reflect.Uint16)

			case '3':
				next(f)
				if next(f) != '2' {
					logpanic("Was expecting u32.", gold)
				}
				kinds = append(kinds, reflect.Uint32)

			case '6':
				next(f)
				if next(f) != '4' {
					logpanic("Was expecting u64.", gold)
				}
				kinds = append(kinds, reflect.Uint64)

			default:
				kinds = append(kinds, reflect.Uint)
			}

		case 'f':
			r := peek(f)
			switch r {
			case '3':
				next(f)
				if next(f) != '2' {
					logpanic("Was expecting f32.", gold)
				}
				kinds = append(kinds, reflect.Float32)

			case '6':
				next(f)
				if next(f) != '4' {
					logpanic("Was expecting f64.", gold)
				}
				kinds = append(kinds, reflect.Float64)

			default:
				logpanic("Expecting either f32 or f64", gold)
			}

		case 'c':
			r := peek(f)
			switch r {
			case '6':
				next(f)
				if next(f) != '4' {
					logpanic("Was expecting c64.", gold)
				}
				kinds = append(kinds, reflect.Complex64)

			case '1':
				next(f)
				if next(f) != '2' {
					logpanic("Was expecting c128.", gold)
				}
				if next(f) != '8' {
					logpanic("Was expecting c128.", gold)
				}
				kinds = append(kinds, reflect.Complex128)

			default:
				logpanic("Expecting either c64 or c128", gold)
			}

		default:
			logpanic("Invalid replace sequence", gold)
		}

		if requireBrace {
			if len(*f) == 0 {
				logpanic("Missing '}' character at end of line", gold)
			}
			if next(f) != '}' {
				logpanic("Missing '}' character", gold)
			}
		}
	}

	segs = append(segs, string(curseg))

	return Logger{
		Kinds: kinds,
	}, segs
}

func peek(s *string) rune {
	r, _ := utf8.DecodeRuneInString(*s)

	if r == utf8.RuneError {
		panic("Malformed log string")
	}

	return r
}

func next(s *string) rune {
	r, n := utf8.DecodeRuneInString(*s)
	*s = (*s)[n:]

	if r == utf8.RuneError {
		panic("Malformed log string")
	}

	return r
}

func logpanic(msg, gold string) {
	panic(fmt.Sprintf("Malformed log format string. %s.\n%s", msg, gold))
}

type handler struct {
	formatString string
	index        uint32
	logger Logger
}

var defaultHandler handler

type Binlog struct {
	constDataBase uint
	constDataSize uint
	handlers      []*handler
	currentIndex  *uint32
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

func (b *Binlog) addHandler(fmt string) {
	var h handler
	h.index := atomic.AddUint32(b.currentIndex, 1) // If I want to start from zero I can add (-1)
	h.formatString = s
	l, segs := parseLogLine(fmt)
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

