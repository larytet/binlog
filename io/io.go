// Package io provides Fifo, the buffer between the logger and the thread which writes the log
// to a file or a socket.
//
// Fifo is a ring of blocks. A producer allocates a block of the exact size it needs, copies
// the data into the block and commits it. A consumer pops committed blocks in the order they
// were allocated. Blocks are contiguous: when the space at the end of the ring is too small
// the producer marks it as skipped and allocates from offset zero.
//
// Any number of producer goroutines and one consumer goroutine are supported. A producer
// reserves its block with one compare and swap of the tail. The producer publishes the block
// with one atomic store of its 32 bit header, which the consumer polls. There is no lock.
// All memory behind the head is zero: the consumer clears every block it releases. A header
// that a producer has not written yet therefore reads as "free", and the consumer waits for it.
// A producer that is stopped between Alloc and Commit holds up the consumer and with it
// every later block, so keep that window short.
package io

import (
	"encoding/binary"
	stdio "io"
	"sync/atomic"
	"unsafe"
)

// Block states, the low 8 bits of the block header. The upper 24 bits are the payload length.
const (
	stateFree  = 0 // not written yet (a reserved block is free until it is committed)
	stateReady = 2 // committed, the consumer may read it
	stateSkip  = 3 // the rest of the ring up to its end is unused
)

const (
	headerSize = 4 // bytes: state (8 bits) and payload length (24 bits)
	align      = 8 // all blocks start at a multiple of align
	// MaxBlock is the largest payload of one block
	MaxBlock = 1<<24 - 1
)

// Fifo is a ring buffer. Use New.
type Fifo struct {
	words []uint64 // backing store, uint64 for 8 byte alignment of the headers
	data  []byte   // the same memory as bytes
	size  uint64   // bytes in the ring, a power of two

	// head and tail count bytes since the start and only grow: the position in the ring
	// is the counter modulo size. tail is advanced by the producers, head by the consumer.
	head    atomic.Uint64
	tail    atomic.Uint64
	dropped atomic.Uint64

	// the partly read block of the stream (Read) interface
	readBuf []byte // backing store, reused
	pending []byte // unread part of readBuf
}

// Block is a reserved area of the ring. Fill Data, then call Commit.
type Block struct {
	Data []byte
	off  uint64
}

// New returns a Fifo of at least size bytes. The usable size is rounded up to a power of two
// (so that a position is a mask, not a division) and is at least 16.
func New(size int) *Fifo {
	n := uint64(2 * align)
	for n < uint64(size) {
		n <<= 1
	}
	f := &Fifo{words: make([]uint64, n/align), size: n}
	f.data = unsafe.Slice((*byte)(unsafe.Pointer(&f.words[0])), f.size)
	return f
}

func blockSize(n int) uint64 {
	return (uint64(n) + headerSize + align - 1) &^ (align - 1)
}

func (f *Fifo) header(off uint64) *uint32 {
	return (*uint32)(unsafe.Pointer(&f.data[off]))
}

func pack(state uint32, length int) uint32 { return state | uint32(length)<<8 }

// Alloc reserves a block with n bytes of payload. It returns false, and counts a drop,
// if the ring is full or n is out of range. Safe for concurrent use by several producers.
func (f *Fifo) Alloc(n int) (Block, bool) {
	if n < 0 || n > MaxBlock {
		f.dropped.Add(1)
		return Block{}, false
	}
	total := blockSize(n)
	for {
		// head first: it never passes a tail that is loaded later
		head := f.head.Load()
		tail := f.tail.Load()
		off := tail & (f.size - 1)
		var skip uint64
		if off+total > f.size {
			skip = f.size - off
		}
		used := tail - head
		if used > f.size {
			// Impossible in a consistent state: head is stale, other producers and the
			// consumer have moved on since it was loaded. Without this check the unsigned
			// subtraction below would wrap and report a huge free space.
			continue
		}
		if total > f.size || f.size-used < skip+total {
			f.dropped.Add(1)
			return Block{}, false
		}
		if !f.tail.CompareAndSwap(tail, tail+skip+total) {
			continue // another producer was first
		}
		if skip != 0 {
			atomic.StoreUint32(f.header(off), pack(stateSkip, 0))
			off = 0
		}
		return Block{Data: f.data[off+headerSize : off+headerSize+uint64(n)], off: off}, true
	}
}

// Commit makes the block visible to the consumer. Call it once, from the goroutine
// that allocated the block.
func (f *Fifo) Commit(b Block) {
	atomic.StoreUint32(f.header(b.off), pack(stateReady, len(b.Data)))
}

// next returns the block at the head of the ring, stepping over skipped space. It returns
// false if the ring is empty. It never reads the memory at the tail: that may hold stale
// payload bytes of an earlier lap and not a header.
func (f *Fifo) next() (head uint64, header uint32, ok bool) {
	for {
		head = f.head.Load()
		if head == f.tail.Load() {
			return 0, 0, false
		}
		off := head & (f.size - 1)
		header = atomic.LoadUint32(f.header(off))
		if header&0xff != stateSkip {
			return head, header, true
		}
		clear(f.data[off:]) // the skipped space up to the end of the ring, including its header
		f.head.Store(head + (f.size - off))
	}
}

// consume appends the payload of the ready block at head to dst and releases the block
func (f *Fifo) consume(head uint64, header uint32, dst []byte) []byte {
	off, n := head%f.size, uint64(header>>8)
	dst = append(dst, f.data[off+headerSize:off+headerSize+n]...)
	total := blockSize(int(n))
	clear(f.data[off : off+total]) // see the package comment: memory behind the head is zero
	f.head.Store(head + total)
	return dst
}

// Pop appends the payload of the oldest committed block to dst and returns it.
// It returns false if there is no committed block: the ring is empty, or the producer has
// reserved the next block and not committed it yet. Consumer only.
func (f *Fifo) Pop(dst []byte) ([]byte, bool) {
	head, header, ok := f.next()
	if !ok || header&0xff != stateReady {
		return dst, false
	}
	return f.consume(head, header, dst), true
}

// Len returns the number of bytes of the ring in use: committed and allocated blocks,
// their headers, padding and skipped space. It is 0 when the ring is empty.
func (f *Fifo) Len() int {
	return int(f.tail.Load() - f.head.Load())
}

// Size returns the number of bytes of the ring
func (f *Fifo) Size() int { return int(f.size) }

// Dropped returns the number of failed allocations
func (f *Fifo) Dropped() uint64 { return f.dropped.Load() }

// ResetDropped sets the drop counter to zero and returns its previous value
func (f *Fifo) ResetDropped() uint64 { return f.dropped.Swap(0) }

// WriteIntegral writes a block holding a little endian value of count (1, 2, 4 or 8) bytes.
// It returns false if the ring is full or count is not supported. Safe for concurrent producers.
func (f *Fifo) WriteIntegral(value uint64, count int) (ok bool) {
	if count != 1 && count != 2 && count != 4 && count != 8 {
		return false
	}
	b, ok := f.Alloc(count)
	if !ok {
		return false
	}
	switch count {
	case 1:
		b.Data[0] = byte(value)
	case 2:
		binary.LittleEndian.PutUint16(b.Data, uint16(value))
	case 4:
		binary.LittleEndian.PutUint32(b.Data, uint32(value))
	case 8:
		binary.LittleEndian.PutUint64(b.Data, value)
	}
	f.Commit(b)
	return true
}

// ReadIntegral reads the oldest block, written by WriteIntegral with the same count.
// It returns false if there is no committed block, or the block has another size
// (the block is then left in place). Consumer only.
func (f *Fifo) ReadIntegral(count int) (value uint64, ok bool) {
	head, header, ok := f.next()
	if !ok || header&0xff != stateReady || int(header>>8) != count {
		return 0, false
	}
	var buf [8]byte
	payload := f.consume(head, header, buf[:0])
	switch count {
	case 1:
		return uint64(payload[0]), true
	case 2:
		return uint64(binary.LittleEndian.Uint16(payload)), true
	case 4:
		return uint64(binary.LittleEndian.Uint32(payload)), true
	case 8:
		return binary.LittleEndian.Uint64(payload), true
	}
	return 0, false
}

// ErrFull is returned by Write when the ring has no room
var ErrFull = stdioError("io.Fifo is full")

type stdioError string

func (e stdioError) Error() string { return string(e) }

// Write makes Fifo an io.Writer: p becomes one block. It returns ErrFull and writes nothing
// if the ring has no room. Safe for concurrent producers, a block is never split.
func (f *Fifo) Write(p []byte) (int, error) {
	b, ok := f.Alloc(len(p))
	if !ok {
		return 0, ErrFull
	}
	copy(b.Data, p)
	f.Commit(b)
	return len(p), nil
}

// Read makes Fifo an io.Reader over the concatenated payloads of the committed blocks.
// It returns io.EOF when there is no committed data: poll again later. Consumer only.
func (f *Fifo) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(f.pending) == 0 {
		var ok bool
		if f.readBuf, ok = f.Pop(f.readBuf[:0]); !ok {
			return 0, stdio.EOF
		}
		f.pending = f.readBuf
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

// Framer connects one logger to a Fifo so that every log entry is one block, and a consumer
// never sees half an entry. Use it as both the io.Writer and the WriterControl of the logger:
//
//	fr := io.NewFramer(fifo)
//	b := binlog.Init(fr, fr, base, size)
//
// The logger calls FrameStart, several Write calls and FrameEnd for every entry. The entry is
// collected in a private buffer and committed to the Fifo at FrameEnd. If the Fifo is full the
// whole entry is dropped (Fifo.Dropped counts it). One Framer serves one logger. Several loggers,
// each with its own Framer, can share one Fifo.
type Framer struct {
	fifo    *Fifo
	buf     []byte
	dropped atomic.Uint64
}

// NewFramer returns a Framer which writes to f
func NewFramer(f *Fifo) *Framer { return &Framer{fifo: f} }

// FrameStart starts a new entry. The argument is the logger's writer and is not used.
func (fr *Framer) FrameStart(stdio.Writer) { fr.buf = fr.buf[:0] }

// Write adds bytes to the entry in progress
func (fr *Framer) Write(p []byte) (int, error) {
	fr.buf = append(fr.buf, p...)
	return len(p), nil
}

// FrameEnd commits the entry as one block. The argument is not used.
func (fr *Framer) FrameEnd(stdio.Writer) {
	if _, err := fr.fifo.Write(fr.buf); err != nil {
		fr.dropped.Add(1) // the Fifo counts it too, but it counts the drops of every producer
	}
}

// Dropped returns the number of entries of this Framer that were dropped because the Fifo was full
func (fr *Framer) Dropped() uint64 { return fr.dropped.Load() }
