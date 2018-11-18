package binlog

import (
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

func align(v uintptr) uintptr {
	mul := uintptr((1 << 3) - 1)
	return (v + mul) & (^uintptr(mul))
}

func getStringSize(s string) uintptr {
	return align(uintptr(len(s)))
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

func getMyPath() (path string, err error) {
	pid := os.Getpid()
	path = fmt.Sprintf("/proc/%d/exe", pid)
	return os.Readlink(path)
}

type Statm struct {
	Size     int64 // total program size (pages)(same as VmSize in status)
	Resident int64 //size of memory portions (pages)(same as VmRSS in status)
	Shared   int   // number of pages that are shared(i.e. backed by a file)
	Trs      uint  // number of pages that are 'code'(not including libs; broken, includes data segment)
	Lrs      int   //number of pages of library(always 0 on 2.6)
	Drs      int   //number of pages of data/stack(including libs; broken, includes library text)
	Dt       int   //number of dirty pages(always 0 on 2.6)
}

// This straight from https://github.com/jandre/procfs/blob/master/util/structparser.go
// ParseStringsIntoStruct expects a pointer to a struct as its
// first argument. It assigns each element from the lines slice
// sequentially to the struct members, parsing each according to
// type. It currently accepts fields of type int, int64, string
// and time.Time (it assumes that values of the latter kind
// are formatted as a clock-tick count since the system start).
//
// Extra lines are ignored.
//
func parseStringsIntoStruct(vi interface{}, strs []string) error {
	v := reflect.ValueOf(vi).Elem()
	typeOf := v.Type()

	for i := 0; i < v.NumField(); i++ {
		if i > len(strs) {
			break
		}
		str := strings.TrimSpace(strs[i])
		interf := v.Field(i).Addr().Interface()
		if err := parseField(interf, str); err != nil {
			return fmt.Errorf("cannot parse field %s=%q: %v", typeOf.Field(i).Name, str, err)
		}
	}
	return nil
}

func parseField(field interface{}, line string) error {
	switch field := field.(type) {
	case *int:
		val, err := strconv.Atoi(line)
		if err != nil {
			return err
		}
		*field = val
	case *int64:
		val, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return err
		}
		*field = val
	case *uint64:
		val, err := strconv.ParseUint(line, 10, 64)
		if err != nil {
			return err
		}
		*field = val
	case *string:
		*field = line

		/*
			case *time.Time:
				jiffies, err := strconv.ParseInt(line, 10, 64)
				if err != nil {
					return err
				}
				*field = jiffiesToTime(jiffies)
			case *time.Duration:
				jiffies, err := strconv.ParseInt(line, 10, 64)
				if err != nil {
					return nil
				}
				*field = jiffiesToDuration(jiffies)
		*/
	default:
		return fmt.Errorf("unsupported field type %T", field)
	}
	return nil
}

// see https://unix.stackexchange.com/questions/224015/memory-usage-of-a-given-process-using-linux-proc-filesystem
// https://www.cyberciti.biz/faq/linux-viewing-process-address-space-command/
// https://www.dennyzhang.com/check_process
// I need /proc/self/maps, /proc/self/status, /proc/self/statm
func getTextSize() (textSize uint, err error) {
	pid := os.Getpid()
	path := fmt.Sprintf("/proc/%d/statm", pid)
	buf, err := ioutil.ReadFile(path)
	if err != nil {
		return 0, err
	}

	lines := strings.Split(string(buf), " ")
	stat := &Statm{}
	err = parseStringsIntoStruct(stat, lines)
	return (4 * 1024 * stat.Trs), err

}

func getTextAddress(textAddress uintptr, err error) {
}

func TestInit(t *testing.T) {
	var constDataBase uintptr
	constDataSize, _ := getTextSize()
	binlog := Init(constDataBase, uint(constDataSize))
	binlog.PrintUint32("PrintUint32 %u", 10)
}
