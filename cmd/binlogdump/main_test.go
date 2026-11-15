package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/larytet/binlog"
)

func TestFormat(t *testing.T) {
	entry := &binlog.LogEntry{Filename: "a.go", LineNumber: 7, FmtString: "x %d", Args: []interface{}{5}, Index: 3, Timestamp: 99}
	if got := format(entry, 0); got != "x 5" {
		t.Errorf("%q", got)
	}
	all := binlog.FlagTimestamp | binlog.FlagLogIndex | binlog.FlagSourceLine
	if got := format(entry, all); got != "99 #3 a.go:7: x 5" {
		t.Errorf("%q", got)
	}
}

func TestUsageAndErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, strings.NewReader(""), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "usage") {
		t.Errorf("no arguments: %d %q", code, errOut.String())
	}
	errOut.Reset()
	if code := run([]string{"-bogus"}, strings.NewReader(""), &out, &errOut); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	errOut.Reset()
	if code := run([]string{"/nonexistent", "-"}, strings.NewReader(""), &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "not a Go executable") {
		t.Errorf("bad executable: %d %q", code, errOut.String())
	}
}
