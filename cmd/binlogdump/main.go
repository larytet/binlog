// Command binlogdump prints a binary log as text.
//
//	binlogdump [-flags N] [-format text|ndjson|es-bulk] [-index NAME] [-sort timestamp|index [-window N]] EXECUTABLE STREAM...
//
// EXECUTABLE is the Go program that wrote the stream, and its sources must be on this machine:
// the format strings and argument types are read from them. STREAM is a file or - for stdin.
// A stream that starts with a header (see binlog.Binlog.WriteHeader) describes itself.
// For a stream without a header give -flags: the sum of 1 string index, 2 source line,
// 4 log index and 8 timestamp.
//
// Several streams are printed one after the other. With -sort they are merged into one list
// ordered by timestamp or index, see binlog.Merge for the meaning of -window. All streams must
// have the optional fields that the key needs.
//
// -format ndjson prints one JSON object per entry. -format es-bulk puts the action line of the
// Elasticsearch bulk API before every object (-index names the index); pipe it to
// curl -H 'Content-Type: application/x-ndjson' --data-binary @- host:9200/_bulk
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/larytet/binlog"
	"github.com/larytet/binlog/ast"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("binlogdump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	flags := fs.Int("flags", 0, "optional fields of a stream without a header: 1 string index, 2 source line, 4 log index, 8 timestamp")
	formatName := fs.String("format", "text", "output format: text, ndjson or es-bulk")
	index := fs.String("index", "binlog", "Elasticsearch index name for -format es-bulk")
	sortName := fs.String("sort", "", "merge the streams ordered by timestamp or index")
	window := fs.Int("window", 1024, "entries held back for sorting, 0 for no limit (the whole input in memory)")
	verbose := fs.Bool("v", false, "report calls of the sources that could not be analysed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	switch *formatName {
	case "text", "ndjson", "es-bulk":
	default:
		fmt.Fprintf(stderr, "binlogdump: unknown format %q, use text, ndjson or es-bulk\n", *formatName)
		return 2
	}
	switch *sortName {
	case "", "timestamp", "index":
	default:
		fmt.Fprintf(stderr, "binlogdump: unknown sort key %q, use timestamp or index\n", *sortName)
		return 2
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(stderr, "usage: binlogdump [-flags N] [-format F] [-sort KEY -window N] [-v] EXECUTABLE STREAM...")
		return 2
	}
	executable := fs.Arg(0)

	table, err := ast.LoadTable(executable)
	if err != nil {
		fmt.Fprintf(stderr, "binlogdump: %v\n", err)
		return 1
	}
	if *verbose {
		for _, d := range table.Diagnostics {
			fmt.Fprintf(stderr, "binlogdump: %v\n", d)
		}
		for _, c := range table.Conflicts {
			fmt.Fprintf(stderr, "binlogdump: hash %08x is used by different calls %q at %v\n", c.Hash, c.Texts, c.Sites)
		}
	}

	var sources []binlog.Source
	for _, name := range fs.Args()[1:] {
		var in io.Reader = stdin
		if name != "-" {
			f, err := os.Open(name)
			if err != nil {
				fmt.Fprintf(stderr, "binlogdump: %v\n", err)
				return 1
			}
			defer f.Close()
			in = f
		}
		reader := bufio.NewReader(in)
		streamFlags := binlog.Flags(*flags)
		if head, err := reader.Peek(len(binlog.StreamMagic)); err == nil && string(head) == binlog.StreamMagic {
			if streamFlags, err = binlog.ReadHeader(reader); err != nil {
				fmt.Fprintf(stderr, "binlogdump: %s: %v\n", name, err)
				return 1
			}
		}
		sources = append(sources, binlog.Source{Reader: reader, Table: table.Handlers, Files: table.Files, Flags: streamFlags})
	}

	out := bufio.NewWriter(stdout)
	defer out.Flush()
	count := 0
	var failure error
	print := func(entry *binlog.LogEntry, streamFlags binlog.Flags) error {
		count++
		switch *formatName {
		case "text":
			fmt.Fprintln(out, format(entry, streamFlags))
		default:
			line, err := formatJSON(entry, streamFlags)
			if err != nil {
				return err
			}
			if *formatName == "es-bulk" {
				action, _ := json.Marshal(map[string]map[string]string{"index": {"_index": *index}})
				fmt.Fprintf(out, "%s\n", action)
			}
			fmt.Fprintf(out, "%s\n", line)
		}
		return nil
	}

	if *sortName != "" {
		for i, source := range sources {
			if source.Flags != sources[0].Flags {
				fmt.Fprintf(stderr, "binlogdump: stream %d has other optional fields than stream 0, they cannot be merged\n", i)
				return 1
			}
		}
		key := binlog.SortByTimestamp
		if *sortName == "index" {
			key = binlog.SortByIndex
		}
		stats, err := binlog.Merge(sources, key, *window, func(e *binlog.LogEntry) error { return print(e, sources[0].Flags) })
		if stats.Late != 0 {
			fmt.Fprintf(stderr, "binlogdump: %d entries arrived too late for the window of %d and are out of order\n", stats.Late, *window)
		}
		failure = err
	} else {
		for _, source := range sources {
			for failure == nil {
				var entry *binlog.LogEntry
				entry, err := binlog.DecodeNextFlags(source.Reader, source.Table, source.Files, source.Flags)
				if err == io.EOF {
					break
				}
				if err != nil {
					failure = fmt.Errorf("entry %d: %v", count+1, err)
					break
				}
				failure = print(entry, source.Flags)
			}
		}
	}
	if failure != nil {
		out.Flush()
		fmt.Fprintf(stderr, "binlogdump: %v\n", failure)
		return 1
	}
	return 0
}

// format prints the optional fields that the stream has, then the formatted text
func format(entry *binlog.LogEntry, flags binlog.Flags) string {
	prefix := ""
	if flags&binlog.FlagTimestamp != 0 {
		prefix += fmt.Sprintf("%d ", entry.Timestamp)
	}
	if flags&binlog.FlagLogIndex != 0 {
		prefix += fmt.Sprintf("#%d ", entry.Index)
	}
	if flags&binlog.FlagSourceLine != 0 {
		prefix += fmt.Sprintf("%s:%d: ", entry.Filename, entry.LineNumber)
	}
	return prefix + fmt.Sprintf(entry.FmtString, entry.Args...)
}

// formatJSON returns the entry as one line of JSON: the optional fields that the stream has,
// the format string, the arguments and the formatted message. JSON has no NaN or infinity, they
// are written as the strings "NaN", "+Inf" and "-Inf". Invalid UTF-8 in a string argument is
// replaced by U+FFFD by encoding/json.
func formatJSON(entry *binlog.LogEntry, flags binlog.Flags) ([]byte, error) {
	args := make([]interface{}, len(entry.Args))
	for i, a := range entry.Args {
		args[i] = a
		switch v := a.(type) {
		case float32:
			args[i] = jsonFloat(float64(v), v)
		case float64:
			args[i] = jsonFloat(v, v)
		}
	}
	doc := map[string]interface{}{
		"message": fmt.Sprintf(entry.FmtString, entry.Args...),
		"format":  entry.FmtString,
		"args":    args,
	}
	if flags&binlog.FlagTimestamp != 0 {
		doc["timestamp"] = entry.Timestamp
	}
	if flags&binlog.FlagLogIndex != 0 {
		doc["index"] = entry.Index
	}
	if flags&binlog.FlagSourceLine != 0 {
		doc["file"] = entry.Filename
		doc["line"] = entry.LineNumber
	}
	return json.Marshal(doc)
}

func jsonFloat(v float64, original interface{}) interface{} {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return original
}
