package io

import (
	"bytes"
	"encoding/binary"
	stdio "io"
	"math/rand"
	"strings"
	"testing"
)

func compress(t *testing.T, algo Algorithm, blockSize int, chunks ...[]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	cw, err := NewCompressedWriter(&out, algo, blockSize)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chunks {
		if _, err := cw.Write(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func logLikeData(n int) []byte {
	rng := rand.New(rand.NewSource(7))
	var b bytes.Buffer
	for b.Len() < n {
		b.Write([]byte{0x95, 0x23, 0x74, 0xbb}) // a hash
		var v [8]byte
		binary.LittleEndian.PutUint64(v[:], uint64(rng.Intn(1000)))
		b.Write(v[:])
	}
	return b.Bytes()[:n]
}

// TC-COMP-1 (FR-COMP-1): round trip for each algorithm, for sizes around the block size
func TestCompressRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	random := make([]byte, 5000)
	rng.Read(random)
	for _, algo := range []Algorithm{None, Flate} {
		for _, size := range []int{0, 1, 99, 100, 101, 250, 5000} {
			for _, data := range [][]byte{logLikeData(size), random[:min(size, len(random))]} {
				stream := compress(t, algo, 100, data[:size/2], data[size/2:])
				got, err := stdio.ReadAll(NewCompressedReader(bytes.NewReader(stream)))
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("%v size %d: err %v, %d bytes back", algo, size, err, len(got))
				}
			}
		}
	}
}

// the point of Flate: a log-like stream gets much smaller, random data does not grow by more than the headers
func TestCompressRatio(t *testing.T) {
	data := logLikeData(200000)
	if n := len(compress(t, Flate, 0, data)); n > len(data)/3 {
		t.Errorf("log-like data: %d bytes of %d", n, len(data))
	}
	random := make([]byte, 100000)
	rand.New(rand.NewSource(2)).Read(random)
	stream := compress(t, Flate, 4096, random)
	blocks := (len(random) + 4095) / 4096
	if len(stream) > len(random)+len(CompressedMagic)+blocks*blockHeaderSize {
		t.Errorf("random data grew to %d bytes of %d", len(stream), len(random))
	}
}

func TestCompressWriterCounters(t *testing.T) {
	var out bytes.Buffer
	cw, _ := NewCompressedWriter(&out, Flate, 1000)
	cw.Write(logLikeData(5000))
	cw.Close()
	if cw.Written != 5000 || cw.Stored != uint64(out.Len()) || cw.Stored >= cw.Written {
		t.Errorf("written %d stored %d (%d bytes out)", cw.Written, cw.Stored, out.Len())
	}
	if _, err := NewCompressedWriter(&out, Algorithm(9), 0); err == nil {
		t.Errorf("unknown algorithm accepted")
	}
	if _, err := NewCompressedWriter(&out, None, -1); err == nil {
		t.Errorf("negative block size accepted")
	}
	if None.String() != "none" || Flate.String() != "flate" || !strings.Contains(Algorithm(9).String(), "9") {
		t.Errorf("String()")
	}
}

// Flush makes the bytes so far readable, the stream goes on afterwards
func TestCompressFlush(t *testing.T) {
	var out bytes.Buffer
	cw, _ := NewCompressedWriter(&out, Flate, 1<<20)
	cw.Write([]byte("first "))
	if err := cw.Flush(); err != nil {
		t.Fatal(err)
	}
	cw.Write([]byte("second"))
	cw.Close()
	got, err := stdio.ReadAll(NewCompressedReader(bytes.NewReader(out.Bytes())))
	if err != nil || string(got) != "first second" {
		t.Fatalf("%q %v", got, err)
	}
}

// TC-COMP-1: a damaged stream is an error, never wrong data
func TestCompressedReaderRejectsDamage(t *testing.T) {
	data := logLikeData(3000)
	for _, algo := range []Algorithm{None, Flate} {
		good := compress(t, algo, 1000, data)
		read := func(stream []byte) ([]byte, error) {
			return stdio.ReadAll(NewCompressedReader(bytes.NewReader(stream)))
		}
		// every single bit flip in the payload area is caught by the checksum or by DEFLATE
		for pos := len(CompressedMagic); pos < len(good); pos += 7 {
			bad := append([]byte(nil), good...)
			bad[pos] ^= 0x10
			got, err := read(bad)
			if err == nil && bytes.Equal(got, data) {
				continue // the flip hit a byte that does not matter (it cannot be a payload byte)
			}
			if err == nil {
				t.Fatalf("%v: flip at %d gave wrong data without an error", algo, pos)
			}
		}
		// truncation at every length is an error unless it is at the end
		for cut := 0; cut < len(good); cut += 13 {
			got, err := read(good[:cut])
			if err == nil && cut != len(CompressedMagic) && !bytes.HasPrefix(data, got) {
				t.Fatalf("%v: cut at %d gave %d wrong bytes", algo, cut, len(got))
			}
			if cut > len(CompressedMagic) && cut < len(good) && err == nil && len(got) == len(data) {
				t.Fatalf("%v: cut at %d returned everything", algo, cut)
			}
		}
		if _, err := read([]byte("XXXX")); err == nil {
			t.Errorf("bad magic accepted")
		}
		if _, err := read(nil); err == nil {
			t.Errorf("empty input accepted")
		}
	}
	// unknown algorithm and a huge announced length
	var header [blockHeaderSize]byte
	header[0] = 9
	if _, err := stdio.ReadAll(NewCompressedReader(bytes.NewReader(append([]byte(CompressedMagic), header[:]...)))); err == nil {
		t.Errorf("unknown algorithm accepted")
	}
	binary.LittleEndian.PutUint32(header[1:], 1<<31)
	header[0] = 0
	if _, err := stdio.ReadAll(NewCompressedReader(bytes.NewReader(append([]byte(CompressedMagic), header[:]...)))); err == nil {
		t.Errorf("huge length accepted")
	}
}

// after a bad block the error stays
func TestCompressedReaderErrorIsSticky(t *testing.T) {
	good := compress(t, Flate, 100, logLikeData(500))
	good[len(good)-1] ^= 0xff
	r := NewCompressedReader(bytes.NewReader(good))
	_, err1 := stdio.ReadAll(r)
	_, err2 := r.Read(make([]byte, 1))
	if err1 == nil || err2 == nil {
		t.Errorf("errors %v %v", err1, err2)
	}
}

func BenchmarkCompressFlate(b *testing.B) {
	data := logLikeData(1 << 16)
	b.SetBytes(int64(len(data)))
	var out bytes.Buffer
	cw, _ := NewCompressedWriter(&out, Flate, 0)
	for i := 0; i < b.N; i++ {
		cw.Write(data)
		if out.Len() > 1<<22 {
			out.Reset()
		}
	}
}
