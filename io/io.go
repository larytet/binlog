package io

// API bytes.Buffer is not fast enough fot the log
// I want a FIFO optimized for wrting 4 and 8 bytes words
// When outputting a block to the binary stream "allocate" the required number of bytes
// from the cyclic buffer, copy the data
// I can do allocation with only one atomic if I need thread safety

type Fifo struct {
	head int
	tail int
	data []byte
	size int
}

func New(size int) *Fifo {
	s := new(Fifo)
	// Alocate one byte more
	// I am going to use s.data[s.size] and save CPU cycles
	s.data = make([]byte, size+1, size+1)
	s.size = size
	s.head = 0
	s.tail = 0
	return s
}

func (s *Fifo) inc(v int) int {
	if v < s.size {
		v += 1
	} else {
		v = 0
	}
	return v
}

func (s *Fifo) WriteIntegral(value uint64, count int) (ok bool) {
	newTail := s.inc(s.tail)
	if s.head != newTail {
		s.data[s.tail] = byte(value) // TODO
		s.tail = newTail
		return true
	} else {
		return false
	}
}

func (s *Fifo) ReadIntegral(count int) (key uint64, ok bool) {
	newHead := s.inc(s.head)
	if s.head != s.tail {
		key = uint64(s.data[s.head]) // TODO
		s.head = newHead
		return key, true
	} else {
		return key, false
	}
}

func (s *Fifo) Len() int {
	if s.head <= s.tail {
		return s.tail - s.head
	} else {
		return s.size - s.head + s.tail
	}
}
