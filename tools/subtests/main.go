// Command subtests reports table-style loops in test functions that do not run each case as a
// subtest.
//
//	subtests [module-root]   exit 1 when a loop is reported
//
// A loop is reported when it ranges over a list of cases written out in the test (a composite
// literal, or a variable the test or the package sets from one), reports through the test's
// *testing.T, and never calls Run. Such a loop names its failing case only if its author remembered
// to print it, ends at the first Fatal, and cannot be run for one case; t.Run gives each case a name,
// its own failure and a -run filter. A loop that is not a table of cases says so with a comment on
// the line of the loop or the line above it: `// subtests: <reason>`.
//
// Test files built only with the live tag are skipped: a failing live case has to stop the test
// before it writes more to a switch. Only Test functions are looked at, not helpers, examples or
// fuzz targets. golangci-lint has no check for this (paralleltest and tparallel ask for t.Parallel,
// which is another rule), so `make lint` runs this command as well.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const usage = "usage: subtests [module-root]\n"

// Exit codes: nothing to report, loops reported, the tool could not do its work.
const (
	exitOK       = 0
	exitReported = 1
	exitUsage    = 2
)

// marker is the comment that exempts a loop; the reason after it is mandatory.
const marker = "subtests:"

// main is the process entry point.
// coverage: ignore - process entry point; run holds all the logic and is tested.
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the whole command: args are the command line without the program name.
func run(args []string, stdout, stderr io.Writer) int {
	root := "."
	switch len(args) {
	case 0:
	case 1:
		root = args[0]
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	found, err := check(root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "subtests:", err)
		return exitUsage
	}
	for _, f := range found {
		_, _ = fmt.Fprintf(stdout, "%s:%d: %s ranges over its cases without t.Run (run each case as a subtest, or explain with a `// %s <reason>` comment)\n",
			f.file, f.line, f.test, marker)
	}
	if len(found) > 0 {
		return exitReported
	}
	return exitOK
}

// finding is one reported loop.
type finding struct {
	file string
	line int
	test string
}

// check parses the test files below root, one package directory at a time, and returns the
// loops it reports in file and line order.
func check(root string) ([]finding, error) {
	dirs := map[string][]string{}
	//nolint:gosec // the directory the operator names on the command line; the walk only reads source files
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var out []finding
	for _, paths := range dirs {
		found, err := checkPackage(paths)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].file != out[j].file {
			return out[i].file < out[j].file
		}
		return out[i].line < out[j].line
	})
	return out, nil
}

// checkPackage reports the loops of the test files of one directory. A package-level variable set
// from a composite literal can sit in any of them, so all are parsed before any is looked at.
func checkPackage(paths []string) ([]finding, error) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	shared := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			if g, ok := d.(*ast.GenDecl); ok {
				collectLiterals(g, shared)
			}
		}
	}
	var out []finding
	for _, f := range files {
		if builtOnlyForLive(f) {
			continue
		}
		exempt := exemptLines(fset, f)
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			out = append(out, checkTest(fset, fn, shared, exempt)...)
		}
	}
	return out, nil
}

// checkTest reports the loops of one Test function.
func checkTest(fset *token.FileSet, fn *ast.FuncDecl, shared map[string]bool, exempt map[int]bool) []finding {
	cases := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch s := n.(type) {
		case *ast.AssignStmt:
			for i, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && i < len(s.Rhs) && isLiteral(s.Rhs[i]) {
					cases[id.Name] = true
				}
			}
		case *ast.GenDecl:
			collectLiterals(s, cases)
		}
		return true
	})
	testing := testingNames(fn)
	var out []finding
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		loop, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		pos := fset.Position(loop.Pos())
		if isCases(loop.X, cases, shared) && mentions(loop.Body, testing) && !callsRun(loop.Body) && !exempt[pos.Line] && !exempt[pos.Line-1] {
			out = append(out, finding{file: pos.Filename, line: pos.Line, test: fn.Name.Name})
		}
		return true
	})
	return out
}

// isLiteral reports whether e is a composite literal.
func isLiteral(e ast.Expr) bool {
	_, ok := e.(*ast.CompositeLit)
	return ok
}

// isCases reports whether a range operand is a list of cases written out in the source: a
// composite literal, or a variable that was set from one.
func isCases(e ast.Expr, local, shared map[string]bool) bool {
	if id, ok := e.(*ast.Ident); ok {
		return local[id.Name] || shared[id.Name]
	}
	return isLiteral(e)
}

// collectLiterals records into set the variables a declaration sets from a composite literal.
func collectLiterals(g *ast.GenDecl, set map[string]bool) {
	for _, spec := range g.Specs {
		v, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range v.Names {
			if i < len(v.Values) && isLiteral(v.Values[i]) {
				set[name.Name] = true
			}
		}
	}
}

// testingNames returns the names of the *testing.T parameters of fn and of the function literals
// inside it (the closure of a subtest has its own).
func testingNames(fn *ast.FuncDecl) map[string]bool {
	names := map[string]bool{}
	ast.Inspect(fn, func(n ast.Node) bool {
		var params *ast.FieldList
		switch f := n.(type) {
		case *ast.FuncDecl:
			params = f.Type.Params
		case *ast.FuncLit:
			params = f.Type.Params
		}
		if params == nil {
			return true
		}
		for _, field := range params.List {
			if isTestingT(field.Type) {
				for _, name := range field.Names {
					names[name.Name] = true
				}
			}
		}
		return true
	})
	return names
}

// isTestingT reports whether a type expression is *testing.T.
func isTestingT(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing"
}

// mentions reports whether the node uses any of the names.
func mentions(n ast.Node, names map[string]bool) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if id, ok := m.(*ast.Ident); ok && names[id.Name] {
			found = true
		}
		return !found
	})
	return found
}

// callsRun reports whether the node calls a method named Run.
func callsRun(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if call, ok := m.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Run" {
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

// builtOnlyForLive reports whether the file's build constraint names the live tag.
func builtOnlyForLive(f *ast.File) bool {
	for _, group := range f.Comments {
		if group.Pos() >= f.Package {
			break
		}
		for _, c := range group.List {
			if constraint, ok := strings.CutPrefix(c.Text, "//go:build "); ok && strings.Contains(constraint, "live") && !strings.Contains(constraint, "!live") {
				return true
			}
		}
	}
	return false
}
