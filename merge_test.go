package binlog

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

// streamOf writes entries with the given timestamps, "stream s #k" as the text
func streamOf(t *testing.T, name string, timestamps []int64) (Source, *Binlog) {
	var buf bytes.Buffer
	base, size := GetSelfTextAddressSize()
	var ts int64
	b := New(Config{&buf, &WriterControlDummy{}, base, size, func() int64 { return ts }})
	withFlags(FlagTimestamp|FlagLogIndex, func() {
		for k, v := range timestamps {
			ts = v
			var err error
			switch name {
			case "a":
				err = b.Log("merge a %d", k)
			case "b":
				err = b.Log("merge b %d", k)
			case "c":
				err = b.Log("merge c %d", k)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	})
	table, files := b.GetIndexTable()
	return Source{Reader: &buf, Table: table, Files: files, Flags: FlagTimestamp | FlagLogIndex}, b
}

func collect(t *testing.T, sources []Source, key SortKey, window int) ([]int64, MergeStats) {
	var keys []int64
	stats, err := Merge(sources, key, window, func(e *LogEntry) error {
		if key == SortByTimestamp {
			keys = append(keys, e.Timestamp)
		} else {
			keys = append(keys, int64(e.Index))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return keys, stats
}

// TC-SORT-1 (FR-SORT-1): ordered streams merge exactly with a window of the number of sources
func TestMergeOrderedStreams(t *testing.T) {
	a, _ := streamOf(t, "a", []int64{1, 4, 7, 10})
	b, _ := streamOf(t, "b", []int64{2, 5, 8})
	c, _ := streamOf(t, "c", []int64{3, 6, 9, 11, 12})
	keys, stats := collect(t, []Source{a, b, c}, SortByTimestamp, 3)
	if !sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }) || len(keys) != 12 {
		t.Fatalf("keys %v", keys)
	}
	if stats.Emitted != 12 || stats.Late != 0 {
		t.Fatalf("stats %+v", stats)
	}
}

// disorder inside one stream: exact if the window is large enough, late entries are counted if not
func TestMergeWindow(t *testing.T) {
	for _, c := range []struct {
		name       string
		timestamps []int64
		window     int
		wantLate   bool
	}{
		{"mild disorder, no limit", []int64{3, 1, 2, 6, 4, 5, 9, 7, 8}, 0, false},
		{"mild disorder, small window", []int64{3, 1, 2, 6, 4, 5, 9, 7, 8}, 3, false},
		{"reversed, no limit", []int64{5, 4, 3, 2, 1}, 0, false},
		{"reversed, window of the whole stream", []int64{5, 4, 3, 2, 1}, 5, false},
		{"reversed, window too small", []int64{5, 4, 3, 2, 1}, 2, true},
	} {
		a, _ := streamOf(t, "a", c.timestamps)
		keys, stats := collect(t, []Source{a}, SortByTimestamp, c.window)
		sorted := sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] })
		if len(keys) != len(c.timestamps) || stats.Emitted != uint64(len(keys)) {
			t.Errorf("%s: keys %v stats %+v", c.name, keys, stats)
		}
		if c.wantLate && (stats.Late == 0 || sorted) {
			t.Errorf("%s: expected late entries, got keys %v stats %+v", c.name, keys, stats)
		}
		if !c.wantLate && (stats.Late != 0 || !sorted) {
			t.Errorf("%s: keys %v stats %+v", c.name, keys, stats)
		}
	}
}

// the index of a process is a global counter, merging by it restores the order of the calls
func TestMergeByIndex(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	base, size := GetSelfTextAddressSize()
	withFlags(FlagLogIndex, func() {
		b1 := Init(&buf1, &WriterControlDummy{}, base, size)
		b2 := Init(&buf2, &WriterControlDummy{}, base, size)
		for i := 0; i < 5; i++ { // alternate: odd indexes to one logger, even to the other
			b1.Log("by index one %d", i)
			b2.Log("by index two %d", i)
		}
		t1, f1 := b1.GetIndexTable()
		t2, f2 := b2.GetIndexTable()
		keys, stats := collect(t, []Source{
			{Reader: &buf2, Table: t2, Files: f2, Flags: FlagLogIndex},
			{Reader: &buf1, Table: t1, Files: f1, Flags: FlagLogIndex},
		}, SortByIndex, 2)
		for i := 1; i < len(keys); i++ {
			if keys[i] != keys[i-1]+1 {
				t.Fatalf("indexes not consecutive: %v", keys)
			}
		}
		if stats.Late != 0 || len(keys) != 10 {
			t.Fatalf("stats %+v keys %v", stats, keys)
		}
	})
}

// a key that the stream does not have, a broken stream and an emit error stop the merge
func TestMergeErrors(t *testing.T) {
	a, _ := streamOf(t, "a", []int64{1, 2})
	noTS := a
	noTS.Flags = 0
	if _, err := Merge([]Source{noTS}, SortByTimestamp, 1, nil); err == nil {
		t.Errorf("no timestamps accepted")
	}
	if _, err := Merge([]Source{noTS}, SortByIndex, 1, nil); err == nil {
		t.Errorf("no index accepted")
	}
	a, _ = streamOf(t, "a", []int64{1, 2})
	if _, err := Merge([]Source{a}, SortByTimestamp, 1, func(*LogEntry) error { return fmt.Errorf("stop") }); err == nil {
		t.Errorf("emit error swallowed")
	}
	bad := Source{Reader: bytes.NewReader([]byte{1, 2, 3, 4}), Table: a.Table, Files: a.Files, Flags: FlagTimestamp}
	if _, err := Merge([]Source{bad}, SortByTimestamp, 1, nil); err == nil {
		t.Errorf("unknown hash accepted")
	}
}
