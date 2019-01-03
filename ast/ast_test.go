package ast

import (
	"os"
	"testing"
)

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
