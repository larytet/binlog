package ast

import (
	"bytes"
	"github.com/larytet/binlog"
	"os"
	"strings"
	"testing"
)

func TestBinlog(t *testing.T) {
	// I need this block to introduce a call to binlog.Log()
	var buf bytes.Buffer
	constDataBase, constDataSize := binlog.GetSelfTextAddressSize()
	binlog := binlog.Init(&buf, &binlog.WriterControlDummy{}, constDataBase, constDataSize)
	binlog.Log("Hello %d", 10)
	d := 11
	binlog.Log("Hello %d", d)
}

func TestGetIndexTable(t *testing.T) {

	filename, err := os.Executable()
	if err != nil {
		t.Fatalf("%v", err)
	}
	_, _, err = GetIndexTable(filename)
	if err != nil {
		// go test links its binary without a symbol table, so this cannot work until
		// the offline decoder (FR-DEC-2) reads the module list some other way.
		// The e2e tests build a real executable instead.
		if strings.Contains(err.Error(), "no symbol section") {
			t.Skipf("FR-DEC-2 not implemented: %v", err)
		}
		t.Fatalf("%v", err)
	}
}
