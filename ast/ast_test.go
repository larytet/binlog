package ast

import (
	"binlog"
	"bytes"
	"os"
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
		t.Fatalf("%v", err)
	}
}
