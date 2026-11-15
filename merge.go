package binlog

import (
	"container/heap"
	"fmt"
	"io"
)

// SortKey selects the field entries are ordered by
type SortKey int

const (
	// SortByTimestamp orders by LogEntry.Timestamp. The streams must have FlagTimestamp.
	SortByTimestamp SortKey = iota
	// SortByIndex orders by LogEntry.Index, the running counter of the process (FlagLogIndex)
	SortByIndex
)

// Source is one binary stream to merge, with what DecodeNextFlags needs
type Source struct {
	Reader io.Reader
	Table  map[uint32]*Handler
	Files  map[uint16]string
	Flags  Flags
}

// MergeStats describes the result of Merge
type MergeStats struct {
	Emitted uint64 // entries passed to emit
	Late    uint64 // entries that arrived after an entry with a larger key was emitted
}

type heapItem struct {
	entry *LogEntry
	key   int64
	seq   uint64 // arrival order: equal keys keep it, so the merge is stable
}

type entryHeap []heapItem

func (h entryHeap) Len() int { return len(h) }
func (h entryHeap) Less(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	return h[i].seq < h[j].seq
}
func (h entryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)   { *h = append(*h, x.(heapItem)) }
func (h *entryHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

// Merge reads the entries of all sources and passes them to emit ordered by key.
//
// Merge reads the sources in turn into a buffer of window entries and emits the smallest
// entry whenever the buffer is full. The order is exact if every entry is read before
// window-1 entries with larger keys have been read after it. Entries of sources that are each in
// order, as the entries of one logger are, are merged exactly with a window of the number of
// sources.
// window <= 0 means no limit: everything is read, then sorted. An entry that arrives too late
// is still emitted, in arrival order, and counted in MergeStats.Late.
//
// A source ends at io.EOF. Any other error of a source stops the merge and is returned
// with the number of the source. An error of emit stops the merge too.
func Merge(sources []Source, key SortKey, window int, emit func(*LogEntry) error) (MergeStats, error) {
	var stats MergeStats
	for i, s := range sources {
		if key == SortByTimestamp && s.Flags&FlagTimestamp == 0 {
			return stats, fmt.Errorf("source %d has no timestamps, it cannot be sorted by timestamp", i)
		}
		if key == SortByIndex && s.Flags&FlagLogIndex == 0 {
			return stats, fmt.Errorf("source %d has no log index, it cannot be sorted by index", i)
		}
	}
	keyOf := func(e *LogEntry) int64 {
		if key == SortByIndex {
			return int64(e.Index)
		}
		return e.Timestamp
	}

	var (
		buffer      entryHeap
		seq         uint64
		lastEmitted int64
		emittedAny  bool
	)
	flush := func(limit int) error {
		for buffer.Len() > limit {
			item := heap.Pop(&buffer).(heapItem)
			if emittedAny && item.key < lastEmitted {
				stats.Late++
			} else {
				lastEmitted = item.key
			}
			emittedAny = true
			stats.Emitted++
			if err := emit(item.entry); err != nil {
				return err
			}
		}
		return nil
	}

	open := make([]bool, len(sources))
	for i := range open {
		open[i] = true
	}
	for remaining := len(sources); remaining > 0; {
		for i := range sources {
			if !open[i] {
				continue
			}
			s := sources[i]
			entry, err := DecodeNextFlags(s.Reader, s.Table, s.Files, s.Flags)
			if err == io.EOF {
				open[i] = false
				remaining--
				continue
			}
			if err != nil {
				return stats, fmt.Errorf("source %d: %v", i, err)
			}
			heap.Push(&buffer, heapItem{entry: entry, key: keyOf(entry), seq: seq})
			seq++
			if window > 0 {
				if err := flush(window - 1); err != nil {
					return stats, err
				}
			}
		}
	}
	return stats, flush(0)
}
