package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// skipDirs are directories no scan enters: version control, build output, fixtures.
var skipDirs = map[string]bool{".git": true, "build": true, "testdata": true, "vendor": true, "node_modules": true}

// devtoolsDir holds the project's own checks; its tests and fixtures are not part of what the
// catalog is checked against for ids.
const devtoolsDir = "devtools"

// analyzerName finds the name an analyzer declares for itself (its `Name` field).
var analyzerName = regexp.MustCompile(`Name:\s*"([A-Za-z0-9_]+)"`)

// repo is what the names of the catalog are resolved against.
type repo struct {
	root string
	// funcs are the top-level functions of the _test.go files: tests, fuzz targets, helpers.
	funcs map[string]bool
	// targets are the targets of the Makefile.
	targets map[string]bool
	// linters are the linters .golangci.yml enables; lintersOK is false when the file has no list.
	linters   map[string]bool
	lintersOK bool
	// devDirs are the directories of devtools; analyzers the names declared in its source.
	devDirs   map[string]bool
	analyzers map[string]bool
	// files are the repository's files, relative to the root, slash-separated.
	files    []string
	problems []Problem
}

// loadRepo walks the repository once and indexes what the catalog may name.
func loadRepo(root string) (*repo, error) {
	r := &repo{root: root, funcs: map[string]bool{}, targets: map[string]bool{}, linters: map[string]bool{},
		devDirs: map[string]bool{}, analyzers: map[string]bool{}}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		r.files = append(r.files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading the repository %s: %w", root, err)
	}
	r.indexTests()
	r.indexDevtools()
	if err := r.indexConfig(); err != nil {
		return nil, err
	}
	return r, nil
}

// indexTests records the top-level functions of every _test.go file.
func (r *repo) indexTests() {
	fset := token.NewFileSet()
	for _, rel := range r.files {
		if !strings.HasSuffix(rel, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(r.root, filepath.FromSlash(rel)), nil, parser.SkipObjectResolution)
		if err != nil {
			r.problems = append(r.problems, Problem{rel, 1, "cannot parse the file, so the tests in it cannot be resolved: " + err.Error()})
		}
		if f == nil {
			continue
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil {
				r.funcs[fn.Name.Name] = true
			}
		}
	}
}

// indexDevtools records the directories of devtools and the analyzer names declared in its source.
func (r *repo) indexDevtools() {
	prefix := devtoolsDir + "/"
	for _, rel := range r.files {
		rest, ok := strings.CutPrefix(rel, prefix)
		if !ok {
			continue
		}
		if dir, _, found := strings.Cut(rest, "/"); found {
			r.devDirs[dir] = true
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, m := range analyzerName.FindAllStringSubmatch(string(data), -1) {
			r.analyzers[m[1]] = true
		}
	}
}

// has reports whether the repository has the file or directory (slash-separated, from the root).
func (r *repo) has(rel string) bool {
	_, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel)))
	return err == nil
}
