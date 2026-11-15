package io

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	stdio "io"
)

// Compression of a binary log stream, done by the goroutine which writes the log to its
// destination (the consumer of a Fifo), never by the goroutine which calls Log().
//
// A compressed stream is the magic "BLZ1" followed by blocks. A block is
//
//	algorithm(1) rawLength(4) storedLength(4) crc32(4) payload(storedLength)
//
// with little endian integers. The CRC is of the uncompressed bytes. Every block is
// independent, so a damaged block does not hurt the following ones, and a block which
// does not get smaller is stored as it is (algorithm None).

// CompressedMagic starts a compressed stream. The last byte is the format version.
const CompressedMagic = "BLZ1"

// Algorithm selects how a block is compressed
type Algorithm byte

const (
	// None stores the block as it is
	None Algorithm = 0
	// Flate compresses the block with DEFLATE (compress/flate, default level)
	Flate Algorithm = 1
)

func (a Algorithm) String() string {
	switch a {
	case None:
		return "none"
	case Flate:
		return "flate"
	}
	return fmt.Sprintf("algorithm(%d)", byte(a))
}

const (
	blockHeaderSize = 13
	// maxRawBlock bounds the memory that a damaged length can make the reader allocate
	maxRawBlock = 1 << 24
	// DefaultBlockSize is the uncompressed size of a block if the caller gives none
	DefaultBlockSize = 64 << 10
)

// CompressedWriter is an io.Writer which compresses what is written to it in blocks
type CompressedWriter struct {
	w         stdio.Writer
	algorithm Algorithm
	blockSize int
	buf       []byte
	out       bytes.Buffer
	flate     *flate.Writer
	err       error
	// Written counts the bytes before compression, Stored the bytes written to w
	Written, Stored uint64
}

// NewCompressedWriter writes the stream magic to w and returns a writer which compresses
// into blocks of blockSize bytes (DefaultBlockSize if it is zero). Call Close or Flush
// at the end: bytes of the last block are held back until then.
func NewCompressedWriter(w stdio.Writer, algorithm Algorithm, blockSize int) (*CompressedWriter, error) {
	if algorithm != None && algorithm != Flate {
		return nil, fmt.Errorf("unknown compression algorithm %v", algorithm)
	}
	if blockSize == 0 {
		blockSize = DefaultBlockSize
	}
	if blockSize < 0 || blockSize > maxRawBlock {
		return nil, fmt.Errorf("block size %d is out of range", blockSize)
	}
	cw := &CompressedWriter{w: w, algorithm: algorithm, blockSize: blockSize}
	if _, err := w.Write([]byte(CompressedMagic)); err != nil {
		return nil, err
	}
	cw.Stored = uint64(len(CompressedMagic))
	return cw, nil
}

// Write buffers p and writes every complete block
func (cw *CompressedWriter) Write(p []byte) (int, error) {
	if cw.err != nil {
		return 0, cw.err
	}
	cw.buf = append(cw.buf, p...)
	for len(cw.buf) >= cw.blockSize {
		if err := cw.writeBlock(cw.buf[:cw.blockSize]); err != nil {
			return 0, err
		}
		cw.buf = cw.buf[:copy(cw.buf, cw.buf[cw.blockSize:])]
	}
	return len(p), nil
}

// Flush writes the bytes held back as a shorter block
func (cw *CompressedWriter) Flush() error {
	if cw.err != nil {
		return cw.err
	}
	if len(cw.buf) == 0 {
		return nil
	}
	err := cw.writeBlock(cw.buf)
	cw.buf = cw.buf[:0]
	return err
}

// Close flushes. It does not close the underlying writer.
func (cw *CompressedWriter) Close() error { return cw.Flush() }

func (cw *CompressedWriter) writeBlock(raw []byte) error {
	algorithm, payload := None, raw
	if cw.algorithm == Flate {
		cw.out.Reset()
		if cw.flate == nil {
			cw.flate, _ = flate.NewWriter(&cw.out, flate.DefaultCompression)
		} else {
			cw.flate.Reset(&cw.out)
		}
		if _, err := cw.flate.Write(raw); err != nil {
			cw.err = err
			return err
		}
		if err := cw.flate.Close(); err != nil {
			cw.err = err
			return err
		}
		if cw.out.Len() < len(raw) { // otherwise store: incompressible data must not grow
			algorithm, payload = Flate, cw.out.Bytes()
		}
	}
	var header [blockHeaderSize]byte
	header[0] = byte(algorithm)
	binary.LittleEndian.PutUint32(header[1:], uint32(len(raw)))
	binary.LittleEndian.PutUint32(header[5:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(header[9:], crc32.ChecksumIEEE(raw))
	if _, err := cw.w.Write(header[:]); err != nil {
		cw.err = err
		return err
	}
	if _, err := cw.w.Write(payload); err != nil {
		cw.err = err
		return err
	}
	cw.Written += uint64(len(raw))
	cw.Stored += uint64(blockHeaderSize + len(payload))
	return nil
}

// CompressedReader is an io.Reader which reads a compressed stream
type CompressedReader struct {
	r       stdio.Reader
	started bool
	block   []byte
	pending []byte
	stored  []byte
	err     error
}

// NewCompressedReader returns a reader of the stream written by a CompressedWriter.
// The magic is checked on the first Read.
func NewCompressedReader(r stdio.Reader) *CompressedReader { return &CompressedReader{r: r} }

// Read returns the uncompressed bytes. A clean end of the stream is io.EOF. A stream that ends
// inside a block is io.ErrUnexpectedEOF, a block with a wrong checksum or length is an error
// that is returned again by every later call.
func (cr *CompressedReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(cr.pending) == 0 {
		if cr.err != nil {
			return 0, cr.err
		}
		cr.err = cr.nextBlock()
	}
	n := copy(p, cr.pending)
	cr.pending = cr.pending[n:]
	return n, nil
}

func (cr *CompressedReader) nextBlock() error {
	if !cr.started {
		var magic [len(CompressedMagic)]byte
		if _, err := stdio.ReadFull(cr.r, magic[:]); err != nil {
			return fmt.Errorf("reading the compressed stream magic: %w", err)
		}
		if string(magic[:]) != CompressedMagic {
			return fmt.Errorf("bad compressed stream magic %q, expected %q", magic[:], CompressedMagic)
		}
		cr.started = true
	}
	var header [blockHeaderSize]byte
	if _, err := stdio.ReadFull(cr.r, header[:]); err != nil {
		return err // io.EOF at a block boundary, io.ErrUnexpectedEOF inside a header
	}
	algorithm := Algorithm(header[0])
	rawLen := binary.LittleEndian.Uint32(header[1:])
	storedLen := binary.LittleEndian.Uint32(header[5:])
	want := binary.LittleEndian.Uint32(header[9:])
	if rawLen > maxRawBlock || storedLen > maxRawBlock+maxRawBlock/8 {
		return fmt.Errorf("block lengths %d/%d are out of range, the stream is damaged", rawLen, storedLen)
	}
	if cap(cr.stored) < int(storedLen) {
		cr.stored = make([]byte, storedLen)
	}
	cr.stored = cr.stored[:storedLen]
	if _, err := stdio.ReadFull(cr.r, cr.stored); err != nil {
		if err == stdio.EOF {
			err = stdio.ErrUnexpectedEOF
		}
		return err
	}
	switch algorithm {
	case None:
		if rawLen != storedLen {
			return fmt.Errorf("stored block of %d bytes claims %d", storedLen, rawLen)
		}
		cr.block = append(cr.block[:0], cr.stored...)
	case Flate:
		fr := flate.NewReader(bytes.NewReader(cr.stored))
		cr.block = cr.block[:0]
		var err error
		if cr.block, err = readAllLimited(fr, cr.block, int(rawLen)); err != nil {
			return fmt.Errorf("decompressing a block: %v", err)
		}
		fr.Close()
	default:
		return fmt.Errorf("unknown compression algorithm %d in the stream", header[0])
	}
	if len(cr.block) != int(rawLen) {
		return fmt.Errorf("block decompressed to %d bytes, expected %d", len(cr.block), rawLen)
	}
	if got := crc32.ChecksumIEEE(cr.block); got != want {
		return fmt.Errorf("block checksum %08x, expected %08x: the stream is damaged", got, want)
	}
	cr.pending = cr.block
	return nil
}

// readAllLimited reads r into dst until EOF, failing if it yields more than limit bytes
func readAllLimited(r stdio.Reader, dst []byte, limit int) ([]byte, error) {
	var chunk [4096]byte
	for {
		n, err := r.Read(chunk[:])
		dst = append(dst, chunk[:n]...)
		if len(dst) > limit {
			return dst, fmt.Errorf("more than the %d bytes that the block header announces", limit)
		}
		if err == stdio.EOF {
			return dst, nil
		}
		if err != nil {
			return dst, err
		}
	}
}
