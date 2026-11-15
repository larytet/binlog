// Package e2e holds the end to end (regression) tests of binlog.
//
// The tests build real executables (go test binaries have no symbol table, so they cannot
// be used for offline decoding), run them, and check the binary logs they produce.
// Test names follow docs/TESTPLAN.md. A case that depends on a feature or a fix that is not
// built yet is skipped with the requirement ID; the skip is removed with the feature.
package e2e

import (
	"bytes"
	"fmt"
	blio "github.com/larytet/binlog/io"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/larytet/binlog"
	"github.com/larytet/binlog/ast"
)

// offlineDecoderReady gates the tests that call ast.GetIndexTable. It was false until the
// offline decoder (FR-DEC-2) was implemented, and stays as a switch for platforms without it.
const offlineDecoderReady = true

// knownBroken lists the cases of the sample program that fail today, with the requirement
// the fix belongs to. Remove an entry in the commit that fixes it.
var knownBroken = map[string]string{}

var buildModes = map[string][]string{
	"default":     nil,
	"no-inlining": {"-gcflags=all=-N -l"},
}

var (
	buildOnce = map[string]*sync.Once{"default": {}, "no-inlining": {}}
	buildExe  = map[string]string{}
	buildErr  = map[string]error{}
	buildMu   sync.Mutex
	buildDir  string
)

func TestMain(m *testing.M) {
	var err error
	buildDir, err = os.MkdirTemp("", "binlog-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	os.RemoveAll(buildDir)
	os.Exit(code)
}

// build compiles the sample program once per mode and returns the path of the executable.
func build(t *testing.T, mode string) string {
	t.Helper()
	buildOnce[mode].Do(func() {
		exe := filepath.Join(buildDir, "prog-"+mode)
		args := append([]string{"build"}, buildModes[mode]...)
		args = append(args, "-o", exe, "./prog")
		out, err := exec.Command("go", args...).CombinedOutput()
		buildMu.Lock()
		defer buildMu.Unlock()
		if err != nil {
			buildErr[mode] = fmt.Errorf("go %v: %v\n%s", args, err, out)
			return
		}
		buildExe[mode] = exe
	})
	buildMu.Lock()
	defer buildMu.Unlock()
	if buildErr[mode] != nil {
		t.Fatal(buildErr[mode])
	}
	return buildExe[mode]
}

// run executes the sample program with the flag mask and returns its output directory.
func run(t *testing.T, mode string, flags int) string {
	t.Helper()
	exe := build(t, mode)
	dir := t.TempDir()
	out, err := exec.Command(exe, "-flags", fmt.Sprint(flags), "-dir", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("%s -flags %d: %v\n%s", exe, flags, err, out)
	}
	return dir
}

// readTable reads a name<TAB>value file written by the sample program.
func readTable(t *testing.T, path string) (names []string, values map[string]string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values = map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		name, value, _ := strings.Cut(line, "\t")
		names = append(names, name)
		values[name] = value
	}
	return names, values
}

func shorten(s string) string {
	if len(s) > 120 {
		return s[:120] + fmt.Sprintf("...(%d bytes)", len(s))
	}
	return s
}

// TC-BLD-1, TC-BLD-2: the module builds and vets in a clean state.
func TestTC_BLD_1_BuildAll(t *testing.T) {
	out, err := exec.Command("go", "build", "../...").CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./...: %v\n%s", err, out)
	}
}

// TC-LOG-2, TC-LOG-3, TC-LOG-5, TC-LOG-11: every case of the sample program round trips
// through the stream and the online decoder, for every flag combination (FR-LOG-6) and
// with and without inlining.
func TestTC_LOG_RoundTrip(t *testing.T) {
	for mode := range buildModes {
		for flags := 0; flags < 16; flags++ {
			mode, flags := mode, flags
			t.Run(fmt.Sprintf("%s/flags=%04b", mode, flags), func(t *testing.T) {
				dir := run(t, mode, flags)
				names, expected := readTable(t, filepath.Join(dir, "expected.txt"))
				_, decoded := readTable(t, filepath.Join(dir, "decoded.txt"))
				_, logErrs := readTable(t, filepath.Join(dir, "errors.txt"))
				for _, name := range names {
					name := name
					t.Run(name, func(t *testing.T) {
						if why, ok := knownBroken[name]; ok {
							t.Skip(why)
						}
						if logErrs[name] != `""` {
							t.Errorf("Log returned an error: %s", logErrs[name])
						}
						if decoded[name] != expected[name] {
							t.Errorf("decoded %s\nwant    %s", shorten(decoded[name]), shorten(expected[name]))
						}
					})
				}
			})
		}
	}
}

// TC-LOG-1: a constant string with no arguments is exactly its 4-byte hash.
func TestTC_LOG_1_ConstNoArgsIsHashOnly(t *testing.T) {
	dir := run(t, "default", 0)
	_, hashes := readTable(t, filepath.Join(dir, "hashes.txt"))
	if len(hashes["const-no-args"]) != 8 {
		t.Fatalf("hash of const-no-args = %q, want 8 hex digits", hashes["const-no-args"])
	}
	stream, err := os.ReadFile(filepath.Join(dir, "stream.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", stream[:4]); got != hashes["const-no-args"] {
		t.Fatalf("first entry starts with %s, want %s", got, hashes["const-no-args"])
	}
}

// getIndexTable runs the offline decoder on the sample program, skipping while it is a stub.
func getIndexTable(t *testing.T) (table map[uint32]struct{}, dir string) {
	t.Helper()
	if !offlineDecoderReady {
		t.Skip("FR-DEC-2 not implemented: ast.GetIndexTable is a stub and moduledata.GetModules crashes (D14)")
	}
	exe := build(t, "default")
	dir = run(t, "default", 0)
	handlers, _, err := ast.GetIndexTable(exe)
	if err != nil {
		t.Fatalf("GetIndexTable(%s): %v", exe, err)
	}
	if handlers == nil {
		t.Skip("FR-DEC-2 not implemented: GetIndexTable returns no table")
	}
	table = map[uint32]struct{}{}
	for hash := range handlers {
		table[hash] = struct{}{}
	}
	return table, dir
}

// TC-DEC-1, TC-DEC-2: the offline table has every call, with the hash the program wrote.
func TestTC_DEC_1_OfflineTableMatchesStream(t *testing.T) {
	table, dir := getIndexTable(t)
	names, hashes := readTable(t, filepath.Join(dir, "hashes.txt"))
	for _, name := range names {
		if _, broken := knownBroken[name]; broken || hashes[name] == "" || strings.HasPrefix(name, "l2-cache") {
			continue // no stream bytes, or built at run time: not in the sources
		}
		var hash uint32
		if _, err := fmt.Sscanf(hashes[name], "%08x", &hash); err != nil {
			t.Fatalf("%s: bad hash %q", name, hashes[name])
		}
		// the stream holds the hash little endian
		hash = hash>>24 | (hash>>8)&0xff00 | (hash<<8)&0xff0000 | hash<<24
		if _, ok := table[hash]; !ok {
			t.Errorf("%s: hash %08x is in the stream but not in the offline index table", name, hash)
		}
	}
}

// TC-DEC-9: a missing file and a file that is not a Go executable return an error.
func TestTC_DEC_9_BadExecutable(t *testing.T) {
	if !offlineDecoderReady {
		t.Skip("FR-DEC-2 not implemented (D14)")
	}
	for _, name := range []string{filepath.Join(t.TempDir(), "missing"), "/etc/hostname"} {
		if _, _, err := ast.GetIndexTable(name); err == nil {
			t.Errorf("GetIndexTable(%s) returned no error", name)
		}
	}
}

// Cases below need sample programs that are written together with the feature.
// They are placeholders that keep the test plan visible in `go test -v`.

// marks returns the line of every "// MARK: name" comment of the progdec source
func marks(t *testing.T) (file string, lines map[string]int) {
	t.Helper()
	file, err := filepath.Abs("progdec/main.go")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	lines = map[string]int{}
	for i, line := range strings.Split(string(data), "\n") {
		if _, name, ok := strings.Cut(line, "// MARK: "); ok {
			lines[strings.TrimSpace(name)] = i + 1
		}
	}
	return file, lines
}

func loadProgdec(t *testing.T) *ast.Table {
	t.Helper()
	exe := filepath.Join(buildDir, "progdec")
	if out, err := exec.Command("go", "build", "-o", exe, "./progdec").CombinedOutput(); err != nil {
		t.Fatalf("go build progdec: %v\n%s", err, out)
	}
	table, err := ast.LoadTable(exe)
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func tableHas(table *ast.Table, fmtStr string) bool {
	h := table.Handlers[binlog.FormatHash(fmtStr)]
	return h != nil && h.FmtString() == fmtStr
}

// TC-DEC-2 (FR-DEC-1, FR-DEC-4, D7): the stream of the sample program decodes with the
// table built from its sources and equals the text the program computed itself
func TestTC_DEC_2_OfflineDecode(t *testing.T) {
	if !offlineDecoderReady {
		t.Skip("FR-DEC-2")
	}
	exe := build(t, "default")
	table, err := ast.LoadTable(exe)
	if err != nil {
		t.Fatal(err)
	}
	for flags := 0; flags < 16; flags++ {
		flags := flags
		t.Run(fmt.Sprintf("flags=%04b", flags), func(t *testing.T) {
			dir := run(t, "default", flags)
			names, expected := readTable(t, filepath.Join(dir, "expected.txt"))
			stream, err := os.ReadFile(filepath.Join(dir, "stream.bin"))
			if err != nil {
				t.Fatal(err)
			}
			reader := bytes.NewReader(stream)
			decoded := 0
			for _, name := range names {
				if strings.HasPrefix(name, "l2-cache") {
					break // built at run time: not in the sources
				}
				entry, err := binlog.DecodeNextFlags(reader, table.Handlers, table.Files, binlog.Flags(flags))
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				want, _ := strconv.Unquote(expected[name])
				if got := fmt.Sprintf(entry.FmtString, entry.Args...); got != want {
					t.Fatalf("%s: decoded %s, want %s", name, shorten(got), shorten(want))
				}
				if flags&2 != 0 && (entry.Filename == "" || entry.LineNumber == 0) {
					t.Errorf("%s: no source position (%q:%d)", name, entry.Filename, entry.LineNumber)
				}
				decoded++
			}
			if decoded == 0 {
				t.Fatal("nothing decoded")
			}
		})
	}
}

// TC-DEC-3 (FR-DEC-5): same text with other types is a conflict, same text and types is one entry
func TestTC_DEC_3_Conflicts(t *testing.T) {
	table := loadProgdec(t)
	file, lines := marks(t)
	var found *ast.Conflict
	for i, c := range table.Conflicts {
		if len(c.Texts) > 0 && c.Texts[0] == "dup %d" {
			found = &table.Conflicts[i]
		}
	}
	if found == nil {
		t.Fatalf("no conflict for \"dup %%d\", conflicts: %+v", table.Conflicts)
	}
	want := map[ast.Site]bool{{File: file, Line: lines["dup32"]}: true, {File: file, Line: lines["dup64"]}: true}
	for _, s := range found.Sites {
		delete(want, s)
	}
	if len(want) != 0 {
		t.Errorf("conflict sites %v miss %v", found.Sites, want)
	}
	for _, c := range table.Conflicts {
		if c.Texts[0] == "same %d" {
			t.Errorf("\"same %%d\" with equal types is reported as a conflict")
		}
	}
	if !tableHas(table, "same %d") {
		t.Errorf("\"same %%d\" is missing")
	}
}

// TC-DEC-4 (FR-DEC-6): calls carry their file and line
func TestTC_DEC_4_FileAndLine(t *testing.T) {
	table := loadProgdec(t)
	file, lines := marks(t)
	for _, c := range []struct{ mark, text string }{{"alias", "alias %d"}, {"field", "field %s"}, {"same1", "same %d"}, {"same2", "same %d"}} {
		found := false
		for _, s := range table.Sites[binlog.FormatHash(c.text)] {
			found = found || s == ast.Site{File: file, Line: lines[c.mark]}
		}
		if !found {
			t.Errorf("%s: site %s:%d not recorded, have %v", c.mark, file, lines[c.mark], table.Sites[binlog.FormatHash(c.text)])
		}
	}
	h := table.Handlers[binlog.FormatHash("alias %d")]
	if h == nil || int(h.LineNumberUint) != lines["alias"] || table.Files[h.FilenameHashUint] != file {
		t.Errorf("handler position is not file %s line %d: %+v", file, lines["alias"], h)
	}
}

// TC-DEC-5 (FR-DEC-7): the logger is found by type under an import alias and in a struct
// field, and a Log method of another type is ignored
func TestTC_DEC_5_AliasAndWrappers(t *testing.T) {
	table := loadProgdec(t)
	for _, text := range []string{"alias %d", "field %s"} {
		if !tableHas(table, text) {
			t.Errorf("%q not found", text)
		}
	}
	if tableHas(table, "other %d") {
		t.Errorf("a Log method of another type was treated as binlog")
	}
}

// TC-DEC-6 (FR-DEC-8): calls that cannot be analysed are reported with file and line
func TestTC_DEC_6_Diagnostics(t *testing.T) {
	table := loadProgdec(t)
	file, lines := marks(t)
	for _, mark := range []string{"nonconst", "iface", "spread"} {
		found := false
		for _, d := range table.Diagnostics {
			found = found || d.Site == ast.Site{File: file, Line: lines[mark]}
		}
		if !found {
			t.Errorf("no diagnostic for %s at line %d; have %v", mark, lines[mark], table.Diagnostics)
		}
	}
}

// TC-DEC-7 (FR-DEC-3): constant expressions are evaluated, floats and runes are typed
func TestTC_DEC_7_ConstantExpression(t *testing.T) {
	table := loadProgdec(t)
	for _, text := range []string{"a b %d", "const-%d", "float %g %c", "typed %d"} {
		if !tableHas(table, text) {
			t.Errorf("%q not found", text)
		}
	}
	h := table.Handlers[binlog.FormatHash("float %g %c")]
	if h == nil || fmt.Sprint(h.ArgKinds()) != "[float64 int32]" {
		t.Errorf("argument kinds of the float call: %+v", h)
	}
}

// TC-DEC-8 (FR-DEC-10): binlogdump prints the stream of the sample program as the text the
// program computed, for every flag combination, and reports the entry it cannot decode
func TestTC_DEC_8_Binlogdump(t *testing.T) {
	dump := filepath.Join(buildDir, "binlogdump")
	if out, err := exec.Command("go", "build", "-o", dump, "../cmd/binlogdump").CombinedOutput(); err != nil {
		t.Fatalf("go build binlogdump: %v\n%s", err, out)
	}
	exe := build(t, "default")
	// each run loads and type checks the sources (seconds), so not every combination
	for _, flags := range []int{0, 2, 8, 15} {
		flags := flags
		t.Run(fmt.Sprintf("flags=%04b", flags), func(t *testing.T) {
			dir := run(t, "default", flags)
			var stdout, stderr bytes.Buffer
			cmd := exec.Command(dump, "-flags", fmt.Sprint(flags), exe, filepath.Join(dir, "stream.bin"))
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			// the l2-cache cases are built at run time, they are not in the sources
			if err == nil || !strings.Contains(stderr.String(), "Failed to find format string hash") {
				t.Fatalf("expected the failure on the first l2-cache entry, got %v: %s", err, stderr.String())
			}
			names, expected := readTable(t, filepath.Join(dir, "expected.txt"))
			lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
			n := 0
			for _, name := range names {
				if strings.HasPrefix(name, "l2-cache") {
					break
				}
				want, _ := strconv.Unquote(expected[name])
				if n >= len(lines) || !strings.HasSuffix(lines[n], want) {
					t.Fatalf("%s: line %d is %s, want it to end with %s", name, n, shorten(lines[min(n, len(lines)-1)]), shorten(want))
				}
				if flags&2 != 0 && !strings.Contains(lines[n], ".go:") {
					t.Fatalf("%s: no source position in %s", name, shorten(lines[n]))
				}
				n++
			}
			if n != len(lines) {
				t.Fatalf("%d lines, want %d", len(lines), n)
			}
		})
	}
}

// TC-DEC-8: a stream with a header needs no -flags, and bad input gives a message and status 1
func TestTC_DEC_8_BinlogdumpHeaderAndErrors(t *testing.T) {
	dump := filepath.Join(buildDir, "binlogdump")
	if _, err := os.Stat(dump); err != nil {
		t.Skip("built by TestTC_DEC_8_Binlogdump")
	}
	// a stream that starts with the header needs no -flags
	exe := build(t, "default")
	dir := t.TempDir()
	if out, err := exec.Command(exe, "-flags", "10", "-header", "-dir", dir).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(dump, exe, filepath.Join(dir, "stream.bin"))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Run() // fails on the l2-cache entry, as above
	first := strings.SplitN(stdout.String(), "\n", 2)[0]
	if !strings.HasPrefix(first, "0 ") || !strings.Contains(first, ".go:") || !strings.HasSuffix(first, "hello") {
		t.Errorf("first line with timestamp and position expected, got %q (stderr %s)", first, stderr.String())
	}
	if out, err := exec.Command(dump, "/nonexistent", "/nonexistent").CombinedOutput(); err == nil || !strings.Contains(string(out), "not a Go executable") {
		t.Errorf("missing executable: %v %s", err, out)
	}
	if out, err := exec.Command(dump).CombinedOutput(); err == nil || !strings.Contains(string(out), "usage") {
		t.Errorf("no arguments: %v %s", err, out)
	}
}

// TC-FIFO-9 (FR-CONC-2): a logger writes through a Framer into a Fifo while another goroutine
// decodes. Every entry arrives whole, in order, none is lost (run with -race).
func TestTC_FIFO_9_LoggerOverFifo(t *testing.T) {
	const entries = 20000
	fifo := blio.New(1 << 14)
	framer := blio.NewFramer(fifo)
	base, size := binlog.GetSelfTextAddressSize()
	logger := binlog.Init(framer, framer, base, size)

	// warm up: the handlers are created now, so the consumer only reads the table afterwards
	logger.Log("fifo %d %s", 0, "warm")
	table, files := logger.GetIndexTable()
	if _, err := binlog.DecodeNextFlags(fifo, table, files, 0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= entries; {
			if err := logger.Log("fifo %d %s", i, "payload"); err != nil {
				t.Errorf("Log: %v", err)
				return
			}
			if fifo.Dropped() != 0 {
				// full: wait for the consumer and log the same entry again
				for fifo.Len() > fifo.Size()/2 {
				}
				fifo.ResetDropped()
				continue
			}
			i++
		}
	}()
	for next := 1; next <= entries; {
		entry, err := binlog.DecodeNextFlags(fifo, table, files, 0)
		if err == io.EOF {
			continue // nothing committed yet
		}
		if err != nil {
			t.Fatalf("entry %d: %v", next, err)
		}
		if got, want := fmt.Sprintf(entry.FmtString, entry.Args...), fmt.Sprintf("fifo %d payload", next); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
		next++
	}
	wg.Wait()
}
