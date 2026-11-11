package main

import (
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// annotations holds the `// <marker> <reason>` comments of one file, by line.
type annotations struct {
	fset  *token.FileSet
	lines map[int]bool
}

// readAnnotations collects the comments of f that carry marker. A comment without a reason is reported
// here, once, and still counts as present: the author is told to write the reason, not to find the
// place again.
func readAnnotations(pass *analysis.Pass, f *ast.File, marker string) *annotations {
	a := &annotations{fset: pass.Fset, lines: map[int]bool{}}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			reason, found := reasonOf(c.Text, marker)
			if !found {
				continue
			}
			if reason == "" {
				pass.Reportf(c.Pos(), "a %s annotation needs a reason: `// %s <reason>`", strings.TrimSuffix(marker, ":"), marker)
			}
			a.lines[pass.Fset.Position(c.Pos()).Line] = true
			// The last line of a comment group is the line above the statement it explains.
			a.lines[pass.Fset.Position(cg.End()).Line] = true
		}
	}
	return a
}

// near reports whether an annotation sits on the line of p or on the line above it.
func (a *annotations) near(p token.Pos) bool {
	line := a.fset.Position(p).Line
	return a.lines[line] || a.lines[line-1]
}

// reasonOf returns the text after marker in a comment, and whether the marker is there at all.
func reasonOf(comment, marker string) (reason string, found bool) {
	i := strings.Index(comment, marker)
	if i < 0 {
		return "", false
	}
	reason = strings.TrimSuffix(strings.TrimSpace(comment[i+len(marker):]), "*/")
	return strings.TrimSpace(reason), true
}

// docHasMarker reports whether the doc comment of a function carries marker (the whole function is explained).
func docHasMarker(doc *ast.CommentGroup, marker string) bool {
	return doc != nil && strings.Contains(doc.Text(), marker)
}

// isTestFile reports whether f is a _test.go file.
func isTestFile(pass *analysis.Pass, f *ast.File) bool {
	return strings.HasSuffix(pass.Fset.File(f.Pos()).Name(), "_test.go")
}
