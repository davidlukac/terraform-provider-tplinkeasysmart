package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// marker is the comment that exempts a loop; the reason after it is mandatory.
const marker = "subtests:"

// Analyzer is the check: see the package comment.
var Analyzer = &analysis.Analyzer{
	Name: "subtests",
	Doc:  "reports a loop over a list of test cases that does not run each case with t.Run",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	literals := literalVars(pass)
	for _, f := range pass.Files {
		if !strings.HasSuffix(pass.Fset.File(f.Pos()).Name(), "_test.go") {
			continue
		}
		exempt := exemptLines(pass.Fset, f)
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && isTest(pass, fn) {
				checkTest(pass, fn, literals, exempt)
			}
		}
	}
	return nil, nil
}

// literalVars returns the variables of the package that are set from a composite literal anywhere
// in its files (a package-level variable can be declared in any of them).
func literalVars(pass *analysis.Pass) map[types.Object]bool {
	set := map[types.Object]bool{}
	mark := func(id *ast.Ident, value ast.Expr) {
		if _, ok := value.(*ast.CompositeLit); !ok {
			return
		}
		if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
			set[obj] = true
		}
	}
	for _, f := range pass.Files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch s := n.(type) {
			case *ast.AssignStmt:
				for i, lhs := range s.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && i < len(s.Rhs) {
						mark(id, s.Rhs[i])
					}
				}
			case *ast.ValueSpec:
				for i, id := range s.Names {
					if i < len(s.Values) {
						mark(id, s.Values[i])
					}
				}
			}
			return true
		})
	}
	return set
}

// isTest reports whether fn is a Test function: a name starting with Test and one *testing.T.
func isTest(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	sig, ok := pass.TypesInfo.Defs[fn.Name].Type().(*types.Signature)
	return ok && sig.Params().Len() == 1 && isTestingT(sig.Params().At(0).Type())
}

// isTestingT reports whether t is *testing.T.
func isTestingT(t types.Type) bool {
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	named, ok := ptr.Elem().(*types.Named)
	return ok && named.Obj().Name() == "T" && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == "testing"
}

// checkTest reports the loops of one Test function.
func checkTest(pass *analysis.Pass, fn *ast.FuncDecl, literals map[types.Object]bool, exempt map[int]bool) {
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		loop, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		line := pass.Fset.Position(loop.Pos()).Line
		if isCases(pass, loop.X, literals) && usesT(pass, loop.Body) && !callsRun(pass, loop.Body) && !exempt[line] && !exempt[line-1] {
			pass.Reportf(loop.Pos(), "%s ranges over its cases without t.Run (run each case as a subtest, or explain with a `// %s <reason>` comment)", fn.Name.Name, marker)
		}
		return true
	})
}

// isCases reports whether a range operand is a list of cases written out in the source: a composite
// literal, or a variable that was set from one.
func isCases(pass *analysis.Pass, e ast.Expr, literals map[types.Object]bool) bool {
	switch x := ast.Unparen(e).(type) {
	case *ast.CompositeLit:
		return true
	case *ast.Ident:
		return literals[pass.TypesInfo.ObjectOf(x)]
	}
	return false
}

// usesT reports whether the node uses a *testing.T (the test's, or the one of a closure inside it).
func usesT(pass *analysis.Pass, n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok {
			if obj, ok := pass.TypesInfo.Uses[id].(*types.Var); ok && isTestingT(obj.Type()) {
				found = true
			}
		}
		return !found
	})
	return found
}

// callsRun reports whether the node calls the Run method of a *testing.T.
func callsRun(pass *analysis.Pass, n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if call, ok := m.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Run" && isTestingT(pass.TypesInfo.TypeOf(sel.X)) {
				found = true
			}
		}
		return !found
	})
	return found
}

// exemptLines returns the lines that carry a `// subtests: <reason>` comment.
func exemptLines(fset *token.FileSet, f *ast.File) map[int]bool {
	lines := map[int]bool{}
	for _, group := range f.Comments {
		for _, c := range group.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if reason, ok := strings.CutPrefix(text, marker); ok && strings.TrimSpace(reason) != "" {
				lines[fset.Position(c.Pos()).Line] = true
			}
		}
	}
	return lines
}
