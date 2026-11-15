package binlog

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden_v1.bin")

func withFlags(f Flags, do func()) {
	s, a, l, ts := SEND_STRING_INDEX, ADD_SOURCE_LINE, SEND_LOG_INDEX, ADD_TIMESTAMP
	defer func() { SEND_STRING_INDEX, ADD_SOURCE_LINE, SEND_LOG_INDEX, ADD_TIMESTAMP = s, a, l, ts }()
	SEND_STRING_INDEX = f&FlagStringIndex != 0
	ADD_SOURCE_LINE = f&FlagSourceLine != 0
	SEND_LOG_INDEX = f&FlagLogIndex != 0
	ADD_TIMESTAMP = f&FlagTimestamp != 0
	do()
}

func fixedTime() int64 { return 1234567890 }

// logSample writes a header and a fixed list of entries and returns the logger and the stream
func logSample(t *testing.T) (*Binlog, []byte) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := New(Config{&buf, &WriterControlDummy{}, base, size, fixedTime})
	if err := b.WriteHeader(); err != nil {
		t.Fatal(err)
	}
	calls := []error{
		b.Log("golden plain"),
		b.Log("golden ints %d %d %x", int8(-1), uint32(7), uint16(0xbeef)),
		b.Log("golden text %s", "héllo"),
		b.Log("golden float %g", 2.5),
	}
	for _, err := range calls {
		if err != nil {
			t.Fatal(err)
		}
	}
	return b, buf.Bytes()
}

// TC-FMT-2 (D7): the flags come from the header, not from the reader's package variables
func TestHeaderFlagsDecouplePeers(t *testing.T) {
	var stream []byte
	var b *Binlog
	withFlags(FlagLogIndex|FlagTimestamp, func() { b, stream = logSample(t) })
	withFlags(0, func() { // a different process: no flags set
		reader := bytes.NewReader(stream)
		flags, err := ReadHeader(reader)
		if err != nil {
			t.Fatal(err)
		}
		if flags != FlagLogIndex|FlagTimestamp {
			t.Fatalf("flags %b", flags)
		}
		table, names := b.GetIndexTable()
		want := []string{"golden plain", "golden ints -1 7 beef", "golden text héllo", "golden float 2.5"}
		for i, w := range want {
			entry, err := DecodeNextFlags(reader, table, names, flags)
			if err != nil {
				t.Fatalf("entry %d: %v", i, err)
			}
			if got := fmt.Sprintf(entry.FmtString, entry.Args...); got != w {
				t.Errorf("entry %d: %q, want %q", i, got, w)
			}
			if entry.Timestamp != 1234567890 {
				t.Errorf("entry %d: timestamp %d", i, entry.Timestamp)
			}
		}
	})
}

// TC-FMT-3: a wrong magic, a short header and unknown flags are rejected
func TestHeaderRejected(t *testing.T) {
	for name, data := range map[string][]byte{
		"empty":         {},
		"short":         []byte("BLG"),
		"wrong magic":   []byte("XXXX\x00\x00\x00\x00"),
		"other version": []byte("BLG2\x00\x00\x00\x00"),
		"unknown flags": []byte("BLG1\xff\x00\x00\x00"),
	} {
		if _, err := ReadHeader(bytes.NewReader(data)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TC-FMT-4: a stream cut at any byte is an error, never a panic or a wrong entry
func TestTruncatedStream(t *testing.T) {
	var stream []byte
	var b *Binlog
	withFlags(FlagStringIndex|FlagTimestamp, func() { b, stream = logSample(t) })
	table, names := b.GetIndexTable()
	// decode the whole stream to learn where the entries end
	reader := bytes.NewReader(stream)
	flags, _ := ReadHeader(reader)
	var ends []int
	for reader.Len() > 0 {
		if _, err := DecodeNextFlags(reader, table, names, flags); err != nil {
			t.Fatal(err)
		}
		ends = append(ends, len(stream)-reader.Len())
	}
	isEnd := map[int]bool{HeaderSize: true}
	for _, e := range ends {
		isEnd[e] = true
	}
	for cut := HeaderSize; cut < len(stream); cut++ {
		reader := bytes.NewReader(stream[HeaderSize:cut])
		var lastErr error
		entries := 0
		for lastErr == nil {
			_, lastErr = DecodeNextFlags(reader, table, names, flags)
			if lastErr == nil {
				entries++
			}
		}
		if !isEnd[cut] && reader.Len() == 0 && entries == len(ends) {
			t.Fatalf("cut at %d decoded every entry", cut)
		}
		if isEnd[cut] && lastErr.Error() != "EOF" {
			t.Errorf("cut at an entry boundary %d: %v, want EOF", cut, lastErr)
		}
		if !isEnd[cut] && lastErr.Error() == "EOF" {
			t.Errorf("cut inside an entry at %d reported as a clean EOF", cut)
		}
	}
}

// TC-FMT-1 (NFR-COMPAT-1): the byte stream of format v1 does not change by accident.
// A deliberate format change updates testdata/golden_v1.bin with: go test -run Golden -update
func TestGoldenStream(t *testing.T) {
	var stream []byte
	withFlags(FlagTimestamp, func() { _, stream = logSample(t) })
	const path = "testdata/golden_v1.bin"
	if *update {
		if err := os.WriteFile(path, stream, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (create it with -update)", err)
	}
	if !bytes.Equal(golden, stream) {
		t.Fatalf("stream differs from %s\n got % x\nwant % x", path, stream, golden)
	}
}
