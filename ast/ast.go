package ast

import (
	"binlog"
	"github.com/larytet-go/moduledata"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"reflect"
	"strings"
)

type binlogCallArg struct {
	argType reflect.Type // type of the argument
	argKind reflect.Kind // "kind" of the argument, for example int32
}

type binlogCall struct {
	pos       token.Pos // position (offset) in the source file
	line      int       // source line number
	fmtString string    // the format string, 1st argument of the binlog.Log()
	args      []binlogCallArg
}

type astVisitor struct {
	callsCollection *[]binlogCall
	astFile         *ast.File
	tokenFileSet    *token.FileSet
}

func (v *astVisitor) Init(astFile *ast.File, tokenFileSet *token.FileSet, callsCollection []binlogCall) {
	v.callsCollection = &callsCollection
	v.tokenFileSet = tokenFileSet
	v.astFile = astFile
}

func collectVariadicArguments(binlogCall *binlogCall, args []ast.Expr) {
	for _, arg := range args[1:] {
		switch argI := (arg).(type) {
		case *ast.BasicLit:
			argType := reflect.TypeOf(argI)
			argKind := argType.Kind()
			binlogCall.args = append(binlogCall.args, binlogCallArg{argType: argType, argKind: argKind})
		}
	}
}

func (v astVisitor) Visit(astNode ast.Node) ast.Visitor {
	if astNode == nil {
		return nil
	}
	var packageName string
	var functionName string
	var args []ast.Expr
	switch astCallExpr := astNode.(type) {
	case *ast.CallExpr:
		switch astSelectExpr := astCallExpr.Fun.(type) {
		case *ast.SelectorExpr:
			switch astSelectExprX := astSelectExpr.X.(type) {
			case *ast.Ident:
				packageName = astSelectExprX.Name
			}
			astSelectExprSel := astSelectExpr.Sel
			functionName = astSelectExprSel.Name
		}
		args = astCallExpr.Args
	}
	if (packageName != "binlog") || (functionName != "Log") {
		return v
	}
	if len(args) < 1 {
		return v
	}
	switch arg0 := (args[0]).(type) {
	case *ast.BasicLit:
		pos := astNode.Pos()
		posValue := v.tokenFileSet.PositionFor(pos, true)
		line := posValue.Line
		binlogCall := binlogCall{pos: pos, fmtString: arg0.Value, line: line}
		//log.Printf("%v", binlogCall)
		collectVariadicArguments(&binlogCall, args)
		*(v.callsCollection) = append(*(v.callsCollection), binlogCall)
	}
	return v
}

func collectBinlogArguments(astFile *ast.File, tokenFileSet *token.FileSet) (*astVisitor, error) {
	callsCollection := make([]binlogCall, 0)
	//decls := astFile.Decls
	var v astVisitor
	(&v).Init(astFile, tokenFileSet, callsCollection)
	ast.Walk(v, astFile)
	return &v, nil
}

// This function is a work in progress, requires walking the Go AST
//
// Depends on debug/elf package, go/parse and go/ast packages
// Given an executable and the source files returns index tables required for decoding
// of the binary logs
// GetIndexTable() parses the ELF file, reads paths of the modules from the executable,
// parses the sources, finds all calls to binlog.Log(), generates hashes of the format
// strings, list of arguments
// See also http://goast.yuroyoro.net/
// https://stackoverflow.com/questions/46115312/use-ast-to-get-all-function-calls-in-a-function
func GetIndexTable(filename string) (map[uint32]*binlog.Handler, map[uint16]string, error) {
	allModules, err := moduledata.GetModules(filename)
	if err != nil {
		return nil, nil, err
	}
	goModules := make([]string, 0)
	for _, module := range allModules {
		if strings.HasSuffix(module, ".go") {
			goModules = append(goModules, module)
		}
	}
	skipped := 0
	log.Printf("Going to process %d Go modules in the %s", len(goModules), filename)
	for _, module := range goModules {
		tokenFileSet := token.NewFileSet()
		astFile, err := parser.ParseFile(tokenFileSet, module, nil, 0)
		if err != nil {
			log.Printf("Skipping %s, %v", module, err)
			skipped++
			continue
		}
		astVisitor, err := collectBinlogArguments(astFile, tokenFileSet)
		collection := *(astVisitor.callsCollection)
		callsCollectionCount := len(collection)
		if callsCollectionCount > 0 {
			log.Printf("Found %d matches %v", callsCollectionCount, collection[0])
		}
	}
	if skipped != 0 {
		log.Printf("Skipped %d modules", skipped)
	}

	return nil, nil, nil
}
