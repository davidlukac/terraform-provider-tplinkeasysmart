package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"

	"golang.org/x/tools/go/analysis"
)

// readwriteMarker is the comment that explains a function that reads and then writes outside the guard.
const readwriteMarker = "readwrite:"

// Defaults of the flags of the readwrite check: the method names of the escp-go client (the names of the
// settings table in settings.go). Another repository passes its own with `-readwrite.readers=...`.
const (
	// defaultReaders matches the methods that read a setting from the switch.
	defaultReaders = `^(Info|LED|Ports|PortStats|LAGs|IGMP|LoopPrevention|PortMirror|MTUVLAN|PortBasedVLAN|Dot1Q|VLANMode|QoS|Bandwidth|StormControl|System|getTLVs|requireNo\w+)$`
	// defaultWriters matches the methods that change a setting (the exported ones start with Set, Delete or
	// Enable, the unexported ones are the single paths the writes go through).
	defaultWriters = `^((Set|Delete|Enable)[A-Z]\w*|setTLVs|setTLVsSent|writeSystem)$`
	// defaultGuards matches the names that make a read and the write after it one critical section.
	defaultGuards = `^(beginModify|withModify|modify)$`
)

// readwriteConfig is what the flags of the readwrite check set.
type readwriteConfig struct {
	readers, writers, guards, receivers string
	tests                               bool
}

// Readwrite is the readwrite check: a function that reads a setting and then writes one, both through
// calls to methods, without holding the modify guard in between.
//
// The shape is the one of escp-go#142 (six methods that read, changed a field and wrote it all back while
// another call did the same) and of the limit guards of #184: the value read can be stale by the time of
// the write. The fix is one critical section around both (`defer c.beginModify()()`), or, when the
// function is correct without it, a `// readwrite: <reason>` in its doc comment.
//
// What counts is data, set with flags (each a regular expression over a method name, so the same check
// serves any client that names its reads and writes by a pattern):
//
//	-readwrite.readers    the methods that read a setting (default: the escp-go client's getters)
//	-readwrite.writers    the methods that change one (default: Set*, Delete*, Enable*, setTLVs and the like)
//	-readwrite.guards     the names that open the critical section: a call of one (`c.beginModify()`), or the
//	                      Lock of a field of that name (`c.modify.Lock()`)
//	-readwrite.receivers  when set, only a call whose receiver type is named like this counts as a reader
//	                      or writer (matched against `pkgpath.Type`, pointers removed); a call on another
//	                      type with a method of the same name is then not mistaken for one. Empty: any
//	-readwrite.tests      also look at _test.go files (default: no)
//
// Only calls of a method (`x.Name(...)`) are steps; a plain function is not, except that a guard name is a
// guard wherever it is called. The guard must come before the first read to count: taking it after the
// read is the same defect. A function is looked at as a whole, closures included, in source order.
var Readwrite = newReadwrite()

func newReadwrite() *analysis.Analyzer {
	cfg := &readwriteConfig{}
	a := &analysis.Analyzer{
		Name: "readwrite",
		Doc:  "reports a function that reads a setting and then writes one without holding the modify guard across both (a read-then-write outside one critical section)",
		Run:  func(pass *analysis.Pass) (any, error) { return runReadwrite(pass, cfg) },
	}
	a.Flags.StringVar(&cfg.readers, "readers", defaultReaders, "regular expression of the method names that read a setting")
	a.Flags.StringVar(&cfg.writers, "writers", defaultWriters, "regular expression of the method names that write one")
	a.Flags.StringVar(&cfg.guards, "guards", defaultGuards, "regular expression of the names that open the critical section (a call, or a field whose Lock is called)")
	a.Flags.StringVar(&cfg.receivers, "receivers", "", "when set, regular expression the type of the receiver (pkgpath.Type) must match for a call to count as a read or a write")
	a.Flags.BoolVar(&cfg.tests, "tests", false, "also check _test.go files")
	return a
}

// readwriteRules is the compiled form of the flags.
type readwriteRules struct {
	readers, writers, guards *regexp.Regexp
	receivers                *regexp.Regexp // nil: any receiver
}

func compileReadwrite(cfg *readwriteConfig) (*readwriteRules, error) {
	r := &readwriteRules{}
	for _, p := range []struct {
		flag, expr string
		into       **regexp.Regexp
	}{{"readers", cfg.readers, &r.readers}, {"writers", cfg.writers, &r.writers}, {"guards", cfg.guards, &r.guards}} {
		re, err := regexp.Compile(p.expr)
		if err != nil {
			return nil, fmt.Errorf("-readwrite.%s: %w", p.flag, err)
		}
		*p.into = re
	}
	if cfg.receivers != "" {
		re, err := regexp.Compile(cfg.receivers)
		if err != nil {
			return nil, fmt.Errorf("-readwrite.receivers: %w", err)
		}
		r.receivers = re
	}
	return r, nil
}

func runReadwrite(pass *analysis.Pass, cfg *readwriteConfig) (any, error) {
	rules, err := compileReadwrite(cfg)
	if err != nil {
		return nil, err
	}
	for _, f := range pass.Files {
		if !cfg.tests && isTestFile(pass, f) {
			continue
		}
		// An annotation with no reason is reported by readAnnotations; the function is excused either way.
		readAnnotations(pass, f, readwriteMarker)
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil && !docHasMarker(fn.Doc, readwriteMarker) {
				checkReadwrite(pass, rules, fn)
			}
		}
	}
	return nil, nil
}

// steps is what one function does, in source order: the position of its first read, of its first write
// after that, and whether the guard was taken before the first read.
type steps struct {
	firstRead, firstWrite token.Pos
	guarded               bool
}

func checkReadwrite(pass *analysis.Pass, rules *readwriteRules, fn *ast.FuncDecl) {
	var s steps
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			rules.observe(pass, call, &s)
		}
		return true
	})
	if s.firstRead == token.NoPos || s.firstWrite == token.NoPos || s.guarded {
		return
	}
	pass.Reportf(fn.Pos(), "%s reads (line %d) and then writes (line %d) without holding the modify guard: the value read can be stale by the time of the write; hold the guard across both, or add `// readwrite: <reason>` to the doc comment",
		fn.Name.Name, pass.Fset.Position(s.firstRead).Line, pass.Fset.Position(s.firstWrite).Line)
}

// observe records what one call is: a guard, a read or a write. Once a write has been seen a later read
// does not count (a read back after a write is not the read the write depends on).
func (r *readwriteRules) observe(pass *analysis.Pass, call *ast.CallExpr, s *steps) {
	if s.firstWrite != token.NoPos {
		return
	}
	name, recv := calleeName(call)
	switch {
	case r.opensGuard(call, name):
		if s.firstRead == token.NoPos {
			s.guarded = true
		}
	case recv == nil: // a plain function is not a step
	case r.writers.MatchString(name) && r.receiverOK(pass, recv):
		s.firstWrite = call.Pos()
	case r.readers.MatchString(name) && r.receiverOK(pass, recv):
		if s.firstRead == token.NoPos {
			s.firstRead = call.Pos()
		}
	}
}

// opensGuard reports whether call is the guard itself (`beginModify()`, `withModify(...)`) or the Lock of
// the field that is the guard (`c.modify.Lock()`).
func (r *readwriteRules) opensGuard(call *ast.CallExpr, name string) bool {
	if r.guards.MatchString(name) {
		return true
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "Lock" && sel.Sel.Name != "RLock") {
		return false
	}
	field, ok := sel.X.(*ast.SelectorExpr)
	return ok && r.guards.MatchString(field.Sel.Name)
}

// receiverOK reports whether the type of recv passes the -receivers filter (always, when none is set).
func (r *readwriteRules) receiverOK(pass *analysis.Pass, recv ast.Expr) bool {
	if r.receivers == nil {
		return true
	}
	t := pass.TypesInfo.TypeOf(recv)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return r.receivers.MatchString(named.Obj().Pkg().Path() + "." + named.Obj().Name())
}

// calleeName returns the name a call is made by and, for a method call, the receiver expression.
func calleeName(call *ast.CallExpr) (name string, recv ast.Expr) {
	switch f := call.Fun.(type) {
	case *ast.SelectorExpr:
		return f.Sel.Name, f.X
	case *ast.Ident:
		return f.Name, nil
	}
	return "", nil
}
