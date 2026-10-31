package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
)

// marker matches a line of a function's doc comment that excludes the function from the policy.
var marker = regexp.MustCompile(`^\s*//\s*coverage:\s*ignore\b(.*)$`)

// exclusion is a function the policy leaves out, by its first and last line, with its reason.
type exclusion struct {
	start, end int
	reason     string
}

// markedFunctions returns the functions of the Go file at path whose doc comment says
// `coverage: ignore - <reason>`, and the problems found on the way: a marker without a reason, a
// file that does not parse. A file that does not exist has no exclusions.
func markedFunctions(path string) ([]exclusion, []string) {
	src, err := os.ReadFile(path) //nolint:gosec // a source file of the module, named by the profile under the operator's own root
	if err != nil {
		return nil, nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, []string{fmt.Sprintf("%s: cannot read the 'coverage: ignore' markers: %v", path, err)}
	}
	var found []exclusion
	var problems []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		reason, marked := "", false
		for _, c := range fn.Doc.List {
			m := marker.FindStringSubmatch(c.Text)
			if m == nil {
				continue
			}
			marked = true
			rest := strings.TrimSpace(m[1])
			reason = strings.TrimSpace(strings.TrimLeft(rest, "- "))
			if !strings.HasPrefix(rest, "-") || reason == "" {
				problems = append(problems, fmt.Sprintf("%s:%d: 'coverage: ignore' needs a reason: // coverage: ignore - <reason>", path, fset.Position(c.Pos()).Line))
			}
		}
		if marked {
			found = append(found, exclusion{start: fset.Position(fn.Pos()).Line, end: fset.Position(fn.End()).Line, reason: reason})
		}
	}
	return found, problems
}
