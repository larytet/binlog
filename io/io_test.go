package io

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"
)

// TC-FIFO-1 (FR-FIFO-1): 1, 2, 4 and 8 byte values
func TestIntegrals(t *testing.T) {
	f := New(256)
	cases := []struct {
		v     uint64
		count int
	}{{0xab, 1}, {0xbeef, 2}, {0xdeadbeef, 4}, {0x0123456789abcdef, 8}, {0, 1}, {1<<64 - 1, 8}}
	for _, c := range cases {
		if !f.WriteIntegral(c.v, c.count) {
			t.Fatalf("write %x/%d failed", c.v, c.count)
		}
	}
	for _, c := range cases {
		got, ok := f.ReadIntegral(c.count)
		if !ok || got != c.v {
			t.Fatalf("read %d bytes: got %x %v, want %x", c.count, got, ok, c.v)
		}
	}
	if _, ok := f.ReadIntegral(1); ok || f.Len() != 0 {
		t.Fatalf("expected an empty fifo, Len=%d", f.Len())
	}
	if f.WriteIntegral(1, 3) {
		t.Fatalf("a 3 byte integer must be rejected")
	}
	f.WriteIntegral(5, 4)
	if _, ok := f.ReadIntegral(8); ok {
		t.Fatalf("reading with the wrong size must fail")
	}
	if v, ok := f.ReadIntegral(4); !ok || v != 5 {
		t.Fatalf("the block must still be there: %v %v", v, ok)
	}
}

// TC-FIFO-2 (FR-FIFO-2): a block never straddles the end, the reader steps over the skip
func TestSkipAtEnd(t *testing.T) {
	f := New(64)
	// 3 blocks of 16 bytes each: 48 of 64 bytes used, then free them
	for i := 0; i < 3; i++ {
		if !f.WriteIntegral(uint64(i), 8) {
			t.Fatal("write")
		}
	}
	for i := 0; i < 3; i++ {
		if v, ok := f.ReadIntegral(8); !ok || v != uint64(i) {
			t.Fatalf("read %d: %v %v", i, v, ok)
		}
	}
	// 40 byte payload needs 48 bytes: only 16 remain before the end, so it skips them
	payload := bytes.Repeat([]byte{7}, 40)
	b, ok := f.Alloc(len(payload))
	if !ok {
		t.Fatal("alloc after wrap failed")
	}
	if len(b.Data) != 40 {
		t.Fatalf("block size %d", len(b.Data))
	}
	copy(b.Data, payload)
	f.Commit(b)
	got, ok := f.Pop(nil)
	if !ok || !bytes.Equal(got, payload) {
		t.Fatalf("pop after skip: %v %v", got, ok)
	}
	if f.Len() != 0 {
		t.Fatalf("Len %d after everything was read", f.Len())
	}
}

// TC-FIFO-3 (FR-FIFO-3): an uncommitted block hides the committed blocks behind it
func TestUncommittedBlocksReading(t *testing.T) {
	f := New(128)
	b1, _ := f.Alloc(4)
	f.WriteIntegral(2, 4) // committed, but allocated after b1
	if _, ok := f.Pop(nil); ok {
		t.Fatalf("read a block ahead of an uncommitted one")
	}
	copy(b1.Data, []byte{1, 0, 0, 0})
	f.Commit(b1)
	for want := uint64(1); want <= 2; want++ {
		if v, ok := f.ReadIntegral(4); !ok || v != want {
			t.Fatalf("got %v %v, want %d", v, ok, want)
		}
	}
}

// TC-FIFO-4 (FR-FIFO-4): a full ring refuses, counts, and keeps the unread data
func TestFull(t *testing.T) {
	f := New(64) // 4 blocks of 16 bytes
	written := 0
	for f.WriteIntegral(uint64(written), 8) {
		written++
	}
	if written != 4 {
		t.Fatalf("wrote %d blocks into 64 bytes", written)
	}
	if f.Dropped() != 1 {
		t.Fatalf("dropped %d, want 1", f.Dropped())
	}
	if _, err := f.Write([]byte{1}); err != ErrFull {
		t.Fatalf("Write on a full fifo: %v", err)
	}
	for i := 0; i < written; i++ {
		if v, ok := f.ReadIntegral(8); !ok || v != uint64(i) {
			t.Fatalf("unread data damaged at %d: %v %v", i, v, ok)
		}
	}
	if !f.WriteIntegral(99, 8) {
		t.Fatalf("no room after reading everything")
	}
	if _, ok := f.Alloc(MaxBlock + 1); ok {
		t.Fatalf("an oversized block was allocated")
	}
	if _, ok := f.Alloc(1000); ok {
		t.Fatalf("a block larger than the ring was allocated")
	}
}

// TC-FIFO-5, TC-FIFO-6 (FR-FIFO-6, FR-FIFO-1): random operations against a model queue
func TestAgainstModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, size := range []int{16, 64, 1024} {
		f := New(size)
		var model [][]byte
		for op := 0; op < 60000; op++ {
			if rng.Intn(2) == 0 {
				n := rng.Intn(min(f.Size(), 100))
				payload := make([]byte, n)
				rng.Read(payload)
				if n%2 == 0 { // exercise Alloc/Commit and Write
					b, ok := f.Alloc(n)
					if ok {
						copy(b.Data, payload)
						f.Commit(b)
					}
					if ok {
						model = append(model, payload)
					}
				} else if _, err := f.Write(payload); err == nil {
					model = append(model, payload)
				}
			} else {
				got, ok := f.Pop(nil)
				if ok != (len(model) > 0) {
					t.Fatalf("size %d op %d: pop ok=%v, model has %d", size, op, ok, len(model))
				}
				if ok {
					if !bytes.Equal(got, model[0]) {
						t.Fatalf("size %d op %d: got %x want %x", size, op, got, model[0])
					}
					model = model[1:]
				}
			}
			// Len is the space of the blocks of the model, plus skipped space at the end
			var blocks uint64
			for _, m := range model {
				blocks += blockSize(len(m))
			}
			if l := uint64(f.Len()); l < blocks || l >= blocks+f.size {
				t.Fatalf("size %d op %d: Len %d with %d bytes of blocks", size, op, l, blocks)
			}
			if len(model) == 0 && f.Len() != 0 {
				// an empty model may leave a skip marker which Pop steps over on its next call
				if _, ok := f.Pop(nil); ok || f.Len() != 0 {
					t.Fatalf("size %d op %d: empty model but Len %d", size, op, f.Len())
				}
			}
		}
	}
}

// TC-FIFO-7 (FR-CONC-2): one producer and one consumer goroutine, no loss, no reorder (run with -race)
func TestProducerConsumer(t *testing.T) {
	const count = 300000
	f := New(4096)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(0); i < count; {
			if f.WriteIntegral(i, 8) {
				i++
			}
		}
	}()
	next := uint64(0)
	for next < count {
		v, ok := f.ReadIntegral(8)
		if !ok {
			continue
		}
		if v != next {
			t.Fatalf("got %d, want %d", v, next)
		}
		next++
	}
	wg.Wait()
	if f.Len() != 0 {
		t.Fatalf("Len %d", f.Len())
	}
}

// TC-FIFO-8 (FR-FIFO-5): several producers, one consumer, every block arrives whole and each
// producer's blocks arrive in order (run with -race)
func TestManyProducers(t *testing.T) {
	const producers, perProducer = 6, 40000
	f := New(8192) // small: the ring wraps and fills up all the time
	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProducer; {
				n := 8 + (i+p)%25 // blocks of 8 to 32 bytes
				b, ok := f.Alloc(n)
				if !ok {
					runtime.Gosched()
					continue
				}
				binary.LittleEndian.PutUint32(b.Data[0:], uint32(p))
				binary.LittleEndian.PutUint32(b.Data[4:], uint32(i))
				for j := 8; j < n; j++ {
					b.Data[j] = byte(p + i + j) // a pattern that a torn block would break
				}
				f.Commit(b)
				i++
			}
		}(p)
	}
	next := make([]int, producers)
	var buf []byte
	for received := 0; received < producers*perProducer; {
		var ok bool
		if buf, ok = f.Pop(buf[:0]); !ok {
			runtime.Gosched()
			continue
		}
		p, i := int(binary.LittleEndian.Uint32(buf)), int(binary.LittleEndian.Uint32(buf[4:]))
		if p >= producers || i != next[p] {
			t.Fatalf("producer %d: got block %d, want %d", p, i, next[p])
		}
		if len(buf) != 8+(i+p)%25 {
			t.Fatalf("producer %d block %d: length %d", p, i, len(buf))
		}
		for j := 8; j < len(buf); j++ {
			if buf[j] != byte(p+i+j) {
				t.Fatalf("producer %d block %d: byte %d is torn", p, i, j)
			}
		}
		next[p]++
		received++
	}
	wg.Wait()
	if _, ok := f.Pop(nil); ok || f.Len() != 0 {
		t.Fatalf("not empty at the end, Len %d", f.Len())
	}
}

// the stream view: Write and Read concatenate
func TestStream(t *testing.T) {
	f := New(256)
	f.Write([]byte("hello "))
	f.Write([]byte("world"))
	buf := make([]byte, 4)
	var got []byte
	for {
		n, err := f.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}
	if string(got) != "hello world" {
		t.Fatalf("%q", got)
	}
}

// TC-PERF-4 (NFR-PERF-4): writing 8 bytes into the Fifo (the timer stops while the consumer drains)
// against a bytes.Buffer. Compare the two ns/op values.
func BenchmarkFifoWriteIntegral(b *testing.B) {
	f := New(1 << 20)
	for i := 0; i < b.N; i++ {
		if !f.WriteIntegral(uint64(i), 8) {
			b.StopTimer()
			for {
				if _, ok := f.ReadIntegral(8); !ok {
					break
				}
			}
			b.StartTimer()
		}
	}
}

func BenchmarkFifoAllocCommit(b *testing.B) {
	f := New(1 << 20)
	for i := 0; i < b.N; i++ {
		blk, ok := f.Alloc(8)
		if !ok {
			b.StopTimer()
			for {
				if _, ok := f.Pop(nil); !ok {
					break
				}
			}
			b.StartTimer()
			continue
		}
		blk.Data[0] = byte(i)
		f.Commit(blk)
	}
}

func BenchmarkBytesBufferWrite8(b *testing.B) {
	var buf bytes.Buffer
	data := make([]byte, 8)
	for i := 0; i < b.N; i++ {
		buf.Write(data)
		if buf.Len() > 1<<20 {
			b.StopTimer()
			buf.Reset()
			b.StartTimer()
		}
	}
}

func Example() {
	f := New(64)
	f.WriteIntegral(42, 4)
	v, _ := f.ReadIntegral(4)
	fmt.Println(v)
	// Output: 42
}
