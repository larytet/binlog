// Command progdec is the source analysed by the offline decoder tests (TC-DEC-3 to TC-DEC-7).
// It is built, not run: the tests read its sources through the executable's file table.
// The line numbers of the marked calls are looked up by the tests from the "// MARK:" comments.
package main

import (
	"bytes"

	bl "github.com/larytet/binlog"
)

// other has a Log method but is not the binlog logger: it must be ignored (FR-DEC-7)
type other struct{}

func (other) Log(format string, args ...interface{}) error { return nil }

type holder struct {
	logger *bl.Binlog // a logger in a struct field
}

const prefix = "const-"

func dynamic() string { return "dyn" }

func main() {
	var buf bytes.Buffer
	l := bl.Init(&buf, &bl.WriterControlDummy{}, 0, 0) // an import alias and a variable of any name
	h := holder{logger: l}
	var o other
	var iface interface{} = 5
	args := []interface{}{1}

	l.Log("alias %d", 1)                    // MARK: alias
	h.logger.Log("field %s", "x")           // MARK: field
	o.Log("other %d", 1)                    // MARK: other
	l.Log("dup %d", uint32(10))             // MARK: dup32
	l.Log("dup %d", uint64(10))             // MARK: dup64
	l.Log("same %d", int8(1))               // MARK: same1
	l.Log("same %d", int8(2))               // MARK: same2
	l.Log("a"+" b %d", 3)                   // MARK: concat
	l.Log(prefix+"%d", 3)                   // MARK: constconcat
	l.Log(dynamic()+" %d", 3)               // MARK: nonconst
	l.Log("iface %v", iface)                // MARK: iface
	l.Log("spread %d", args...)             // MARK: spread
	l.Log("float %g %c", 1.5, 'x')          // MARK: float
	l.Log("typed %d", struct{ a int }{1}.a) // MARK: field-int
}
