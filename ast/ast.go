// Package ast builds the index table that decodes a binary log offline.
//
// Given the executable that wrote the log, it finds the Go sources the executable was built
// from, finds every call of (*binlog.Binlog).Log, and builds the same handlers the logger
// builds at run time: format string, argument types, hash.
//
// The sources are located through the file table of the executable (.gopclntab), and parsed
// and type checked with go/packages, so the `go` tool and the sources must be available.
package ast

import (
	"debug/buildinfo"
	"debug/elf"
	"debug/gosym"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/larytet/binlog"
)

// binlogPkgPath is the import path of the logger, calls are recognised by their callee type
const binlogPkgPath = "github.com/larytet/binlog"

// Site is the position of a call in the sources
type Site struct {
	File string
	Line int
}

func (s Site) String() string { return fmt.Sprintf("%s:%d", s.File, s.Line) }

// Diagnostic describes a call that could not be added to the table
type Diagnostic struct {
	Site Site
	Msg  string
}

func (d Diagnostic) String() string { return fmt.Sprintf("%s: %s", d.Site, d.Msg) }

// Conflict is a hash used by calls that cannot share a handler: the same format
// string with different argument types (the runtime hash cannot tell them apart), or two
// different format strings with the same hash
type Conflict struct {
	Hash  uint32
	Sites []Site
	Texts []string
}

// Table is the result of the analysis
type Table struct {
	Handlers    map[uint32]*binlog.Handler
	Files       map[uint16]string // hash of the file name (ADD_SOURCE_LINE) to the file name
	Sites       map[uint32][]Site // every call site of a hash
	Conflicts   []Conflict
	Diagnostics []Diagnostic
}

// GetIndexTable returns the maps DecodeNext needs. It returns an error if the file is not
// a Go executable, or its sources cannot be found. Calls that cannot be analysed are
// reported by LoadTable, not here.
func GetIndexTable(filename string) (map[uint32]*binlog.Handler, map[uint16]string, error) {
	table, err := LoadTable(filename)
	if err != nil {
		return nil, nil, err
	}
	return table.Handlers, table.Files, nil
}

// LoadTable analyses the executable and its sources
func LoadTable(filename string) (*Table, error) {
	roots, err := sourceRoots(filename)
	if err != nil {
		return nil, err
	}
	table := newTable()
	for _, root := range roots {
		if err := table.addModule(root); err != nil {
			return nil, err
		}
	}
	table.finish()
	return table, nil
}

// LoadSources analyses the Go packages matched by patterns in dir, without an executable
func LoadSources(dir string, patterns ...string) (*Table, error) {
	table := newTable()
	if err := table.addPackages(dir, patterns...); err != nil {
		return nil, err
	}
	table.finish()
	return table, nil
}

func newTable() *Table {
	return &Table{
		Handlers: map[uint32]*binlog.Handler{},
		Files:    map[uint16]string{},
		Sites:    map[uint32][]Site{},
	}
}

// sourceRoots returns the module directories of the Go files linked into the executable,
// excluding the standard library and the module cache
func sourceRoots(filename string) ([]string, error) {
	if _, err := buildinfo.ReadFile(filename); err != nil {
		return nil, fmt.Errorf("%s is not a Go executable: %v", filename, err)
	}
	f, err := elf.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", filename, err)
	}
	defer f.Close()
	pclntab, text := f.Section(".gopclntab"), f.Section(".text")
	if pclntab == nil || text == nil {
		return nil, fmt.Errorf("%s has no .gopclntab or .text section", filename)
	}
	data, err := pclntab.Data()
	if err != nil {
		return nil, err
	}
	table, err := gosym.NewTable(nil, gosym.NewLineTable(data, text.Addr))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", filename, err)
	}
	skip := []string{runtime.GOROOT(), goEnv("GOMODCACHE")}
	roots := map[string]bool{}
	for file := range table.Files {
		if !strings.HasSuffix(file, ".go") || !filepath.IsAbs(file) {
			continue
		}
		skipped := false
		for _, s := range skip {
			if s != "" && strings.HasPrefix(file, s+string(filepath.Separator)) {
				skipped = true
			}
		}
		if skipped {
			continue
		}
		if root := moduleRoot(filepath.Dir(file)); root != "" {
			roots[root] = true
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("%s: found no sources of the executable on this machine", filename)
	}
	result := make([]string, 0, len(roots))
	for r := range roots {
		result = append(result, r)
	}
	sort.Strings(result)
	return result, nil
}

func moduleRoot(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (t *Table) addModule(root string) error {
	return t.addPackages(root, "./...")
}

func (t *Table) addPackages(dir string, patterns ...string) error {
	cfg := &packages.Config{
		Dir:   dir,
		Tests: true, // calls in _test.go files are linked into test executables
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports | packages.NeedDeps,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return fmt.Errorf("loading %v in %s: %v", patterns, dir, err)
	}
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			// the sources may not compile on this machine, report and go on
			t.Diagnostics = append(t.Diagnostics, Diagnostic{Msg: "package " + pkg.PkgPath + ": " + e.Msg})
		}
		for _, file := range pkg.Syntax {
			t.visitFile(pkg, file)
		}
	}
	return nil
}

func (t *Table) diagnose(site Site, format string, args ...interface{}) {
	d := Diagnostic{Site: site, Msg: fmt.Sprintf(format, args...)}
	for _, known := range t.Diagnostics {
		if known == d {
			return
		}
	}
	t.Diagnostics = append(t.Diagnostics, d)
}

func (t *Table) visitFile(pkg *packages.Package, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isBinlogLog(pkg.TypesInfo, call) {
			return true
		}
		pos := pkg.Fset.PositionFor(call.Pos(), true)
		t.addCall(pkg, call, Site{File: pos.Filename, Line: pos.Line})
		return true
	})
}

// isBinlogLog reports whether the call is (*binlog.Binlog).Log, resolved by type
// so that import aliases and logger variables of any name are found
func isBinlogLog(info *types.Info, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Log" {
		return false
	}
	selection, ok := info.Selections[sel]
	if !ok {
		return false
	}
	fn, ok := selection.Obj().(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != binlogPkgPath {
		return false
	}
	recv := fn.Type().(*types.Signature).Recv()
	if recv == nil {
		return false
	}
	typ := recv.Type()
	if ptr, ok := typ.(*types.Pointer); ok {
		typ = ptr.Elem()
	}
	named, ok := typ.(*types.Named)
	return ok && named.Obj().Name() == "Binlog"
}

func (t *Table) addCall(pkg *packages.Package, call *ast.CallExpr, site Site) {
	if len(call.Args) == 0 {
		t.diagnose(site, "Log called without a format string")
		return
	}
	if call.Ellipsis.IsValid() {
		t.diagnose(site, "arguments passed as args..., their types are unknown")
		return
	}
	tv, ok := pkg.TypesInfo.Types[call.Args[0]]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		t.diagnose(site, "the format string is not a constant, it is decoded only if it is logged from a constant elsewhere")
		return
	}
	fmtStr := constant.StringVal(tv.Value)
	argTypes := make([]reflect.Type, 0, len(call.Args)-1)
	for i, arg := range call.Args[1:] {
		rt, err := reflectType(pkg.TypesInfo.TypeOf(arg))
		if err != nil {
			t.diagnose(site, "argument #%d of %q: %v", i+1, fmtStr, err)
			return
		}
		argTypes = append(argTypes, rt)
	}
	h, err := binlog.NewHandler(fmtStr, argTypes)
	if err != nil {
		t.diagnose(site, "%q: %v", fmtStr, err)
		return
	}
	filenameHash := binlog.FilenameHash(site.File)
	if name, taken := t.Files[filenameHash]; taken && name != site.File {
		t.diagnose(site, "the 16 bit file name hash %04x is shared with %s", filenameHash, name)
	}
	t.Files[filenameHash] = site.File
	h.FilenameHashUint = filenameHash
	h.LineNumberUint = uint16(site.Line)

	for _, known := range t.Sites[h.HashUint] {
		if known == site {
			return // the package and its test variant are both loaded
		}
	}
	t.Sites[h.HashUint] = append(t.Sites[h.HashUint], site)
	if prev, exists := t.Handlers[h.HashUint]; exists {
		if prev.FmtString() == h.FmtString() && reflect.DeepEqual(prev.ArgKinds(), h.ArgKinds()) {
			return // the same call text and types: one entry
		}
		t.recordConflict(prev, h)
		return
	}
	t.Handlers[h.HashUint] = h
}

func (t *Table) recordConflict(prev, h *binlog.Handler) {
	for i := range t.Conflicts {
		if t.Conflicts[i].Hash == h.HashUint {
			return // finish() fills the sites
		}
	}
	t.Conflicts = append(t.Conflicts, Conflict{Hash: h.HashUint, Texts: []string{prev.FmtString(), h.FmtString()}})
}

func (t *Table) finish() {
	for i := range t.Conflicts {
		t.Conflicts[i].Sites = t.Sites[t.Conflicts[i].Hash]
	}
	sort.Slice(t.Conflicts, func(i, j int) bool { return t.Conflicts[i].Hash < t.Conflicts[j].Hash })
}

// reflectType maps the static type of a call argument to the type the logger sees at run time
func reflectType(t types.Type) (reflect.Type, error) {
	if t == nil {
		return nil, fmt.Errorf("no type information")
	}
	basic, ok := t.Underlying().(*types.Basic)
	if !ok {
		return nil, fmt.Errorf("type %s is not supported, its dynamic type is not known statically", t)
	}
	switch basic.Kind() {
	case types.Int, types.UntypedInt:
		return reflect.TypeOf(int(0)), nil
	case types.Int8:
		return reflect.TypeOf(int8(0)), nil
	case types.Int16:
		return reflect.TypeOf(int16(0)), nil
	case types.Int32, types.UntypedRune:
		return reflect.TypeOf(int32(0)), nil
	case types.Int64:
		return reflect.TypeOf(int64(0)), nil
	case types.Uint:
		return reflect.TypeOf(uint(0)), nil
	case types.Uint8:
		return reflect.TypeOf(uint8(0)), nil
	case types.Uint16:
		return reflect.TypeOf(uint16(0)), nil
	case types.Uint32:
		return reflect.TypeOf(uint32(0)), nil
	case types.Uint64:
		return reflect.TypeOf(uint64(0)), nil
	case types.Float32:
		return reflect.TypeOf(float32(0)), nil
	case types.Float64, types.UntypedFloat:
		return reflect.TypeOf(float64(0)), nil
	case types.String, types.UntypedString:
		return reflect.TypeOf(""), nil
	}
	return nil, fmt.Errorf("type %s is not supported", t)
}

// goEnv returns `go env name`, empty if the go tool is not available
func goEnv(name string) string {
	out, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
