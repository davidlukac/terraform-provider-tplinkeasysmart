package main

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// nosleepMarker is the comment that explains a wait on the clock; the reason after it is mandatory.
const nosleepMarker = "nosleep:"

// failingTestMethods are the methods of a testing value that end or fail a test: a `time.After` case whose
// body calls one of them is the bound of a wait, not the wait.
var failingTestMethods = map[string]bool{
	"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true,
	"Skip": true, "Skipf": true, "SkipNow": true,
}

// Nosleep is the nosleep check: a test, or code, that waits for a guess of how long something takes.
//
// A sleep, or a `time.After` that is the expected end of a wait, is a guess: when it is too short the test
// flakes on a slow machine, and when it is too long it is slow; worse, a guess that is wrong does not fail,
// it lets the test pass without having reached the path it names (the escp-go flakes of 0.6.0 to 0.9.0 were
// of this kind). The wait has an event that ends it (a channel, a context, a signal from a seam such as the
// socket wrapper or the write gate, an observed state), and the test waits for that. It reports:
//
//   - a call of time.Sleep, in a test or in production code;
//   - in a _test.go file, a call of time.After that is not the receive of a select case whose body fails the
//     test (a t.Fatal, t.Error, ... or a panic, as a statement of the case itself, not inside an if): such a
//     case is the bound of a wait that has a signal, and the clock is the failure. A time.After that is the way out of a select without failing (a "nothing
//     happened in 100 ms, so it must be waiting") or a bare `<-time.After(d)` is the guess.
//
// An intended wait on the clock carries its reason:
//
//	// nosleep: <reason>
//
// on the call's line, on the line above it, or in the function's doc comment (the whole function). An
// annotation without a reason is itself reported. Write what the test needs the real clock for (a deadline
// behavior through a real socket, a poll of a state that has no signal, bounded).
//
// What it does not see: a wait on a channel that nothing ever sends to, a long timeout used as if it were
// the event, a timer made with time.NewTimer, time.Tick or time.AfterFunc, and a helper that returns before
// what it started has finished. Those are read in review.
var Nosleep = newNosleep()

func newNosleep() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: "nosleep",
		Doc:  "reports time.Sleep, and in a test a time.After that is not the failure bound of a select case, that carries no `// nosleep: <reason>` comment",
		Run:  runNosleep,
	}
}

func runNosleep(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		s := &sleepScan{
			pass:   pass,
			notes:  readAnnotations(pass, f, nosleepMarker),
			test:   isTestFile(pass, f),
			bounds: map[*ast.CallExpr]bool{},
		}
		for _, decl := range f.Decls {
			s.excused = false
			if fn, ok := decl.(*ast.FuncDecl); ok {
				s.excused = docHasMarker(fn.Doc, nosleepMarker)
			}
			ast.Inspect(decl, s.visit)
		}
	}
	return nil, nil
}

// sleepScan walks one file of the pass.
type sleepScan struct {
	pass    *analysis.Pass
	notes   *annotations
	test    bool
	excused bool // the doc comment of the function being walked carries the marker
	// bounds holds the time.After calls that are the receive of a select case that fails the test.
	bounds map[*ast.CallExpr]bool
}

func (s *sleepScan) visit(n ast.Node) bool {
	switch n := n.(type) {
	case *ast.CommClause:
		if call := receivedTimeAfter(s.pass, n); call != nil && failsTheTest(s.pass, n.Body) {
			s.bounds[call] = true
		}
	case *ast.CallExpr:
		s.call(n)
	}
	return true
}

func (s *sleepScan) call(call *ast.CallExpr) {
	if s.excused || s.notes.near(call.Pos()) {
		return
	}
	switch {
	case isTimeFunc(s.pass, call, "Sleep"):
		s.pass.Reportf(call.Pos(), "time.Sleep waits for a guess of how long something takes: wait for the event that ends the wait (a channel, a context, a signal from a seam, an observed state), or explain the real-time wait with `// nosleep: <reason>`")
	case s.test && isTimeFunc(s.pass, call, "After") && !s.bounds[call]:
		s.pass.Reportf(call.Pos(), "time.After in a test that is not the failure bound of a select case (a case that calls t.Fatal or t.Error): a wait that ends on the clock is a guess; wait for the event, or explain it with `// nosleep: <reason>`")
	}
}

// receivedTimeAfter returns the time.After call that a select case receives from (`case <-time.After(d):`
// or `case v := <-time.After(d):`), or nil.
func receivedTimeAfter(pass *analysis.Pass, cc *ast.CommClause) *ast.CallExpr {
	var rx ast.Expr
	switch c := cc.Comm.(type) {
	case *ast.ExprStmt:
		rx = c.X
	case *ast.AssignStmt:
		if len(c.Rhs) == 1 {
			rx = c.Rhs[0]
		}
	}
	un, ok := rx.(*ast.UnaryExpr)
	if !ok {
		return nil
	}
	call, ok := un.X.(*ast.CallExpr)
	if !ok || !isTimeFunc(pass, call, "After") {
		return nil
	}
	return call
}

// failsTheTest reports whether the statements of a select case, at their own level (not inside an if or a
// loop: a failure there is about something else), call a method that fails or ends a test on a testing
// value (one with a Helper method: testing.T, B, F and TB), or panic.
func failsTheTest(pass *analysis.Pass, body []ast.Stmt) bool {
	for _, st := range body {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if failingTestMethods[fun.Sel.Name] && hasMethod(pass.TypesInfo.TypeOf(fun.X), "Helper") {
				return true
			}
		case *ast.Ident:
			if _, builtin := pass.TypesInfo.Uses[fun].(*types.Builtin); builtin && fun.Name == "panic" {
				return true
			}
		}
	}
	return false
}

// isTimeFunc reports whether call is a call of the function of the time package with that name (not a
// method of time.Time that has the same name, such as Time.After).
func isTimeFunc(pass *analysis.Pass, call *ast.CallExpr, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	fn, ok := pass.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok || fn.Pkg() == nil || fn.Pkg().Path() != "time" {
		return false
	}
	return fn.Signature().Recv() == nil
}
