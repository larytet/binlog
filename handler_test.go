package binlog

import (
	"bytes"
	"reflect"
	"testing"
)

// NewHandler is what the offline decoder uses: same hash as the runtime, errors for bad formats
func TestNewHandler(t *testing.T) {
	h, err := NewHandler("x %d %s %g", []reflect.Type{reflect.TypeOf(int8(0)), reflect.TypeOf(""), reflect.TypeOf(0.0)})
	if err != nil {
		t.Fatal(err)
	}
	if h.HashUint != FormatHash("x %d %s %g") || h.FmtString() != "x %d %s %g" {
		t.Errorf("bad handler %+v", h)
	}
	if got := h.ArgKinds(); !reflect.DeepEqual(got, []reflect.Kind{reflect.Int8, reflect.String, reflect.Float64}) {
		t.Errorf("kinds %v", got)
	}
	if _, err := NewHandler("x %d", nil); err == nil {
		t.Errorf("missing argument accepted")
	}
	if _, err := NewHandler("x", []reflect.Type{reflect.TypeOf(1)}); err == nil {
		t.Errorf("extra argument accepted")
	}
	if _, err := NewHandler("x %d", []reflect.Type{reflect.TypeOf("")}); err == nil {
		t.Errorf("%%d with a string accepted")
	}
}

// the runtime and NewHandler agree on the hash that is written to the stream
func TestHashMatchesStream(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	if err := b.Log("hash check"); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler("hash check", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), h.hash) {
		t.Fatalf("stream % x, handler hash % x", buf.Bytes(), h.hash)
	}
	if FilenameHash("a.go") != FilenameHash("a.go") || FilenameHash("a.go") == FilenameHash("b.go") {
		t.Errorf("file name hash is not stable or collides")
	}
}

func TestStatisticsAndL2(t *testing.T) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	b := Init(&buf, &WriterControlDummy{}, base, size)
	for i := 0; i < 3; i++ {
		b.Log("stat %d", i)
	}
	s := b.GetStatistics()
	if s.L1CacheMiss != 1 || s.L1CacheHit != 2 {
		t.Errorf("statistics %+v", s)
	}
	if TimestampDummy() != 0 {
		t.Errorf("TimestampDummy is not zero")
	}
}
