// Command prog is the sample program of the e2e tests.
//
// It logs a fixed list of calls with binlog, one buffer per case. For each case it writes
//   - the name and the text fmt.Sprintf produces for the same call (expected.txt)
//   - the text decoded from the binary stream with the online index table (decoded.txt)
//
// and the concatenated binary streams (stream.bin).
// The e2e test builds this program, runs it for several flag combinations and compares.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/larytet/binlog"
)

type result struct {
	name     string
	expected string
	decoded  string
	logErr   string
	hash     string // first 4 bytes of the entry: the hash of the format string
}

var (
	results []result
	stream  bytes.Buffer
	buf     bytes.Buffer
	logger  *binlog.Binlog
)

// logCase calls do(), which must make exactly one binlog call, then decodes the entry.
func logCase(name, expected string, do func() error) {
	buf.Reset()
	r := result{name: name, expected: expected}
	if err := do(); err != nil {
		r.logErr = err.Error()
	}
	if buf.Len() >= 4 {
		r.hash = fmt.Sprintf("%x", buf.Bytes()[:4])
	}
	stream.Write(buf.Bytes())
	entry, err := logger.DecodeNext(&buf)
	if err != nil {
		r.decoded = "DECODE ERROR: " + err.Error()
	} else {
		r.decoded = fmt.Sprintf(entry.FmtString, entry.Args...)
	}
	results = append(results, r)
}

func main() {
	mask := flag.Int("flags", 0, "bit 0 string index, 1 source line, 2 log index, 3 timestamp")
	dir := flag.String("dir", ".", "output directory")
	header := flag.Bool("header", false, "start stream.bin with the stream header")
	flag.Parse()
	binlog.SEND_STRING_INDEX = *mask&1 != 0
	binlog.ADD_SOURCE_LINE = *mask&2 != 0
	binlog.SEND_LOG_INDEX = *mask&4 != 0
	binlog.ADD_TIMESTAMP = *mask&8 != 0

	base, size := binlog.GetSelfTextAddressSize()
	logger = binlog.Init(&buf, &binlog.WriterControlDummy{}, base, size)

	if *header {
		if err := logger.WriteHeader(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		stream.Write(buf.Bytes())
		buf.Reset()
	}

	// Every call below uses its own literal: the L1 cache is keyed by the literal's address.
	logCase("const-no-args", "hello", func() error { return logger.Log("hello") })
	logCase("percent-literal", "100% sure 7", func() error { return logger.Log("100%% sure %d", 7) })

	logCase("int", "int 10", func() error { return logger.Log("int %d", 10) })
	logCase("int-negative", "int-neg -10", func() error { return logger.Log("int-neg %d", -10) })
	logCase("int8-min", fmt.Sprintf("int8 %d", math.MinInt8), func() error { return logger.Log("int8 %d", int8(math.MinInt8)) })
	logCase("int8-max", fmt.Sprintf("int8max %d", math.MaxInt8), func() error { return logger.Log("int8max %d", int8(math.MaxInt8)) })
	logCase("int16-min", fmt.Sprintf("int16 %d", math.MinInt16), func() error { return logger.Log("int16 %d", int16(math.MinInt16)) })
	logCase("int32-min", fmt.Sprintf("int32 %d", math.MinInt32), func() error { return logger.Log("int32 %d", int32(math.MinInt32)) })
	logCase("int64-min", fmt.Sprintf("int64 %d", int64(math.MinInt64)), func() error { return logger.Log("int64 %d", int64(math.MinInt64)) })
	logCase("int64-max", fmt.Sprintf("int64max %d", int64(math.MaxInt64)), func() error { return logger.Log("int64max %d", int64(math.MaxInt64)) })
	logCase("uint8-max", "uint8 255", func() error { return logger.Log("uint8 %d", uint8(math.MaxUint8)) })
	logCase("uint16-max", "uint16 65535", func() error { return logger.Log("uint16 %d", uint16(math.MaxUint16)) })
	logCase("uint32-max", "uint32 4294967295", func() error { return logger.Log("uint32 %d", uint32(math.MaxUint32)) })
	logCase("uint64-max", fmt.Sprintf("uint64 %d", uint64(math.MaxUint64)), func() error { return logger.Log("uint64 %d", uint64(math.MaxUint64)) })
	logCase("uint-zero", "uint 0", func() error { return logger.Log("uint %d", uint(0)) })
	logCase("hex", "hex deadbeef", func() error { return logger.Log("hex %x", uint32(0xdeadbeef)) })
	logCase("two-ints", "pair 1 2", func() error { return logger.Log("pair %d %d", 1, 2) })
	logCase("mixed-widths", "mix 1 2 3 4", func() error {
		return logger.Log("mix %d %d %d %d", int8(1), int16(2), int32(3), int64(4))
	})

	logCase("float64-pi", "f64 3.141592653589793", func() error { return logger.Log("f64 %g", math.Pi) })
	logCase("float32", "f32 1.500000", func() error { return logger.Log("f32 %f", float32(1.5)) })
	logCase("float64-exp", "f64e 1.000000e+100", func() error { return logger.Log("f64e %e", 1e100) })
	logCase("float64-neg-zero", "nz -0", func() error { return logger.Log("nz %g", math.Copysign(0, -1)) })
	logCase("float64-nan", "nan NaN", func() error { return logger.Log("nan %g", math.NaN()) })
	logCase("float64-inf", "inf +Inf", func() error { return logger.Log("inf %g", math.Inf(1)) })
	logCase("float64-max", fmt.Sprintf("fmax %g", math.MaxFloat64), func() error { return logger.Log("fmax %g", math.MaxFloat64) })
	logCase("float32-smallest", fmt.Sprintf("fmin %g", float32(math.SmallestNonzeroFloat32)), func() error {
		return logger.Log("fmin %g", float32(math.SmallestNonzeroFloat32))
	})
	logCase("char-ascii", "chr x", func() error { return logger.Log("chr %c", 'x') })
	logCase("char-multibyte", "chr2 世", func() error { return logger.Log("chr2 %c", '世') })
	logCase("char-max", fmt.Sprintf("chr3 %c", rune(0x10FFFF)), func() error { return logger.Log("chr3 %c", rune(0x10FFFF)) })

	logCase("string-empty", "str [] end", func() error { return logger.Log("str [%s] end", "") })
	logCase("string-short", "str2 [abc] end", func() error { return logger.Log("str2 [%s] end", "abc") })
	nul := "a\x00b\xffc"
	logCase("string-nul-binary", "bin ["+nul+"]", func() error { return logger.Log("bin [%s]", nul) })
	long := strings.Repeat("x", 65535)
	logCase("string-65535", "long ["+long+"]", func() error { return logger.Log("long [%s]", long) })
	huge := strings.Repeat("y", 70000)
	logCase("string-70000", "huge ["+huge+"]", func() error { return logger.Log("huge [%s]", huge) })
	logCase("string-and-int", "who bob 42", func() error { return logger.Log("who %s %d", "bob", 42) })

	dyn := fmt.Sprintf("%s %%d", "dynamic")
	logCase("l2-cache", "dynamic 5", func() error { return logger.Log(dyn, 5) })
	logCase("l2-cache-again", "dynamic 6", func() error { return logger.Log(dyn, 6) })

	var exp, dec, errs, hashes bytes.Buffer
	for _, r := range results {
		fmt.Fprintf(&exp, "%s\t%q\n", r.name, r.expected)
		fmt.Fprintf(&dec, "%s\t%q\n", r.name, r.decoded)
		fmt.Fprintf(&errs, "%s\t%q\n", r.name, r.logErr)
		fmt.Fprintf(&hashes, "%s\t%s\n", r.name, r.hash)
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(*dir, name), data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	write("expected.txt", exp.Bytes())
	write("decoded.txt", dec.Bytes())
	write("errors.txt", errs.Bytes())
	write("hashes.txt", hashes.Bytes())
	write("stream.bin", stream.Bytes())
}
