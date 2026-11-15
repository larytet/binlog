package main

import (
	"bytes"
	"encoding/json"
	"math"
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

// FR-INT-1: one valid JSON object per entry, fields match the entry
func TestFormatJSON(t *testing.T) {
	entry := &binlog.LogEntry{Filename: "a.go", LineNumber: 7, FmtString: "x %d %s %g %g", Args: []interface{}{int8(-5), "h\"é", math.NaN(), float32(1.5)}, Index: 3, Timestamp: 99}
	line, err := formatJSON(entry, binlog.FlagTimestamp|binlog.FlagLogIndex|binlog.FlagSourceLine)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(line, &doc); err != nil {
		t.Fatalf("invalid JSON %s: %v", line, err)
	}
	args := doc["args"].([]interface{})
	if doc["message"] != "x -5 h\"é NaN 1.5" || doc["format"] != "x %d %s %g %g" || doc["file"] != "a.go" ||
		doc["line"] != 7.0 || doc["timestamp"] != 99.0 || doc["index"] != 3.0 ||
		args[0] != -5.0 || args[1] != "h\"é" || args[2] != "NaN" || args[3] != 1.5 {
		t.Errorf("fields differ: %s", line)
	}
	line, _ = formatJSON(entry, 0)
	var plain map[string]interface{}
	json.Unmarshal(line, &plain)
	if _, has := plain["timestamp"]; has {
		t.Errorf("timestamp present without the flag: %s", line)
	}
	if bytes.Contains(line, []byte("\n")) {
		t.Errorf("a JSON line must not contain a newline")
	}
}

func TestUnknownFormat(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-format", "xml", "a", "b"}, strings.NewReader(""), &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "unknown format") {
		t.Errorf("%d %q", code, errOut.String())
	}
}
