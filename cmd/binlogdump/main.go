// Command binlogdump prints a binary log as text.
//
//	binlogdump [-flags N] EXECUTABLE STREAM
//
// EXECUTABLE is the Go program that wrote the stream, and its sources must be on this machine:
// the format strings and argument types are read from them. STREAM is a file or - for stdin.
// A stream that starts with a header (see binlog.Binlog.WriteHeader) describes itself.
// For a stream without a header give -flags: the sum of 1 string index, 2 source line,
// 4 log index and 8 timestamp.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
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
	verbose := fs.Bool("v", false, "report calls of the sources that could not be analysed")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: binlogdump [-flags N] [-v] EXECUTABLE STREAM")
		return 2
	}
	executable, streamName := fs.Arg(0), fs.Arg(1)

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

	var in io.Reader = stdin
	if streamName != "-" {
		f, err := os.Open(streamName)
		if err != nil {
			fmt.Fprintf(stderr, "binlogdump: %v\n", err)
			return 1
		}
		defer f.Close()
		in = f
	}
	reader := bufio.NewReader(in)
	out := bufio.NewWriter(stdout)
	defer out.Flush()

	streamFlags := binlog.Flags(*flags)
	if head, err := reader.Peek(len(binlog.StreamMagic)); err == nil && string(head) == binlog.StreamMagic {
		if streamFlags, err = binlog.ReadHeader(reader); err != nil {
			fmt.Fprintf(stderr, "binlogdump: %v\n", err)
			return 1
		}
	}

	for count := 1; ; count++ {
		entry, err := binlog.DecodeNextFlags(reader, table.Handlers, table.Files, streamFlags)
		if err == io.EOF {
			return 0
		}
		if err != nil {
			out.Flush()
			fmt.Fprintf(stderr, "binlogdump: entry %d: %v\n", count, err)
			return 1
		}
		fmt.Fprintln(out, format(entry, streamFlags))
	}
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
