package ast

import (
	"bytes"
	"os"
	"testing"

	"github.com/larytet/binlog"
)

func TestBinlog(t *testing.T) {
	// I need this block to introduce calls to binlog.Log() into the test executable
	var buf bytes.Buffer
	constDataBase, constDataSize := binlog.GetSelfTextAddressSize()
	binlog := binlog.Init(&buf, &binlog.WriterControlDummy{}, constDataBase, constDataSize)
	binlog.Log("Hello %d", 10)
	d := 11
	binlog.Log("Hello again %d", d)
}

// FR-DEC-2: the index table of this very test executable contains the calls above
func TestGetIndexTable(t *testing.T) {
	filename, err := os.Executable()
	if err != nil {
		t.Fatalf("%v", err)
	}
	handlers, files, err := GetIndexTable(filename)
	if err != nil {
		t.Fatalf("%v", err)
	}
	for _, text := range []string{"Hello %d", "Hello again %d"} {
		h := handlers[binlog.FormatHash(text)]
		if h == nil || h.FmtString() != text {
			t.Errorf("%q is not in the index table of %d handlers", text, len(handlers))
		}
	}
	if len(files) == 0 {
		t.Errorf("no file names in the table")
	}
}

// FR-DEC-2: errors, not panics, for files that are not Go executables
func TestGetIndexTableBadFiles(t *testing.T) {
	for _, name := range []string{"/nonexistent/file", "/etc/hostname", "/bin/true"} {
		if _, _, err := GetIndexTable(name); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
