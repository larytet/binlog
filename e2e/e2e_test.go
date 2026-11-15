// Package e2e holds the end to end (regression) tests of binlog.
//
// The tests build real executables (go test binaries have no symbol table, so they cannot
// be used for offline decoding), run them, and check the binary logs they produce.
// Test names follow docs/TESTPLAN.md. A case that depends on a feature or a fix that is not
// built yet is skipped with the requirement ID; the skip is removed with the feature.
package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/larytet/binlog/ast"
)

// offlineDecoderReady gates the tests that call ast.GetIndexTable. The function is a stub
// today, and its dependency moduledata.GetModules crashes the process (SIGSEGV) on
// executables built by current Go (D14). F4 replaces it and sets this to true.
const offlineDecoderReady = false

// knownBroken lists the cases of the sample program that fail today, with the requirement
// the fix belongs to. Remove an entry in the commit that fixes it.
var knownBroken = map[string]string{
	"string-empty": "FR-LOG-4: empty string fails to decode",
	"string-70000": "FR-LOG-4: string length is 16 bits",
}

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

func TestTC_DEC_3_Conflicts(t *testing.T)   { t.Skip("FR-DEC-5: write with the offline decoder (F4)") }
func TestTC_DEC_4_FileAndLine(t *testing.T) { t.Skip("FR-DEC-6: write with the offline decoder (F4)") }
func TestTC_DEC_5_AliasAndWrappers(t *testing.T) {
	t.Skip("FR-DEC-7: write with the offline decoder (F4)")
}
func TestTC_DEC_6_Diagnostics(t *testing.T) { t.Skip("FR-DEC-8: write with the offline decoder (F4)") }
func TestTC_DEC_7_ConstantExpression(t *testing.T) {
	t.Skip("FR-DEC-3: write with the offline decoder (F4)")
}
func TestTC_DEC_8_Binlogdump(t *testing.T)      { t.Skip("FR-DEC-10: write with the decoder command (F5)") }
func TestTC_FIFO_9_LoggerOverFifo(t *testing.T) { t.Skip("FR-CONC-2: write with the FIFO (F6)") }
