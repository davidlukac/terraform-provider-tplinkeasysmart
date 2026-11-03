package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// write lays out files (path relative to the returned root, content) and returns the root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// source wraps the body of a test function in a file.
func source(body string) string {
	return "package p\n\nimport \"testing\"\n\n" + body + "\n"
}

func TestWhatIsReported(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want int // loops reported
	}{
		"a literal list reported through t": {source(`func TestA(t *testing.T) {
	for _, in := range []string{"a", "b"} {
		if in == "" {
			t.Errorf("empty")
		}
	}
}`), 1},
		"a local variable set from a literal": {source(`func TestA(t *testing.T) {
	cases := []int{1, 2}
	for _, in := range cases {
		t.Log(in)
	}
}`), 1},
		"a local var declaration": {source(`func TestA(t *testing.T) {
	type kind int
	var cases = map[string]int{"a": 1}
	for name := range cases {
		t.Log(name)
	}
}`), 1},
		"a variable declared without a literal is not a list of cases": {source(`func TestA(t *testing.T) {
	var cases []int
	cases = load()
	for _, in := range cases {
		t.Log(in)
	}
}

func load() []int { return nil }`), 0},
		"the result of a call is not written out in the test": {source(`func TestA(t *testing.T) {
	for _, in := range load() {
		t.Log(in)
	}
}

func load() []int { return nil }`), 0},
		"a package-level list": {source(`var table = []int{1}

func TestA(t *testing.T) {
	for _, in := range table {
		t.Log(in)
	}
}`), 1},
		"a loop with t.Run": {source(`func TestA(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Run(in, func(t *testing.T) { t.Log(in) })
	}
}`), 0},
		"a loop that never uses t": {source(`func TestA(t *testing.T) {
	n := 0
	for range []string{"a"} {
		n++
	}
	t.Log(n)
}`), 0},
		"a loop in a closure with its own t": {source(`func TestA(t *testing.T) {
	t.Run("x", func(sub *testing.T) {
		for _, in := range []string{"a"} {
			sub.Log(in)
		}
	})
}`), 1},
		"a helper, an example and a fuzz target are not looked at": {source(`func helper(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}

func ExampleX() {
	for range []string{"a"} {
	}
}

func FuzzX(f *testing.F) {
	for _, in := range []string{"a"} {
		f.Add(in)
	}
}`), 0},
		"parameters that are not a testing.T": {source(`func TestA(t *testing.T, a int, b *int, c *testing.B, d *other.T) {
	for _, in := range []string{"a"} {
		_ = in
	}
}`), 0},
		"a body-less declaration": {source(`func TestA(t *testing.T)`), 0},
		"a comment with a reason above the loop": {source(`func TestA(t *testing.T) {
	// subtests: the cases share one switch
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}`), 0},
		"a comment with a reason on the line": {source(`func TestA(t *testing.T) {
	for _, in := range []string{"a"} { // subtests: one assertion over all
		t.Log(in)
	}
}`), 0},
		"a comment without a reason": {source(`func TestA(t *testing.T) {
	// subtests:
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}`), 1},
		"another comment": {source(`func TestA(t *testing.T) {
	// the cases
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}`), 1},
		"a live-only file": {"//go:build live\n\n" + source(`func TestA(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}`), 0},
		"a file built without live": {"// Copyright.\n\n//go:build !live\n\n" + source(`func TestA(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}`), 1},
		"a file with another constraint": {"//go:build linux\n\n" + source(`func TestA(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}`), 1},
	} {
		t.Run(name, func(t *testing.T) {
			root := write(t, map[string]string{"a_test.go": tc.src})
			got, err := check(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d reported, want %d: %+v", len(got), tc.want, got)
			}
		})
	}
}

func TestAListSetInAnotherFileOfThePackageIsFound(t *testing.T) {
	root := write(t, map[string]string{
		"a_test.go": "package p\n\nvar table = []int{1}\n",
		"b_test.go": source("func TestB(t *testing.T) {\n\tfor _, in := range table {\n\t\tt.Log(in)\n\t}\n}"),
	})
	got, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].test != "TestB" || got[0].line != 6 || filepath.Base(got[0].file) != "b_test.go" {
		t.Fatalf("got %+v", got)
	}
}

func TestAListSetInAnotherPackageIsNotAssumed(t *testing.T) {
	root := write(t, map[string]string{
		"a/a_test.go": "package a\n\nvar table = []int{1}\n",
		"b/b_test.go": source("func TestB(t *testing.T) {\n\tfor _, in := range table {\n\t\tt.Log(in)\n\t}\n}"),
	})
	got, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestFindingsComeInFileAndLineOrder(t *testing.T) {
	loop := "\tfor _, in := range []string{\"a\"} {\n\t\tt.Log(in)\n\t}\n"
	root := write(t, map[string]string{
		"z_test.go":     source("func TestZ(t *testing.T) {\n" + loop + "}"),
		"a_test.go":     source("func TestA2(t *testing.T) {\n" + loop + loop + "}"),
		"sub/b_test.go": source("func TestB(t *testing.T) {\n" + loop + "}"),
	})
	got, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, f := range got {
		order = append(order, filepath.Base(f.file)+":"+f.test)
	}
	if want := []string{"a_test.go:TestA2", "a_test.go:TestA2", "b_test.go:TestB", "z_test.go:TestZ"}; !slices.Equal(order, want) {
		t.Fatalf("got %q, want %q", order, want)
	}
	if got[0].line >= got[1].line {
		t.Fatalf("lines %d and %d are not in order", got[0].line, got[1].line)
	}
}

func TestHiddenTestdataAndBuildDirectoriesAreNotLooked(t *testing.T) {
	bad := source("func TestA(t *testing.T) {\n\tfor _, in := range []string{\"a\"} {\n\t\tt.Log(in)\n\t}\n}")
	root := write(t, map[string]string{
		".hidden/a_test.go":  bad,
		"testdata/a_test.go": bad,
		"build/a_test.go":    bad,
		"notatest.go":        "package p\n",
	})
	got, err := check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestARootThatStartsWithADotIsLooked(t *testing.T) {
	bad := source("func TestA(t *testing.T) {\n\tfor _, in := range []string{\"a\"} {\n\t\tt.Log(in)\n\t}\n}")
	root := write(t, map[string]string{"a_test.go": bad})
	t.Chdir(root)
	got, err := check(".")
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestAFileThatDoesNotParseStopsTheRun(t *testing.T) {
	root := write(t, map[string]string{"a_test.go": "package p\n\nfunc {"})
	if _, err := check(root); err == nil {
		t.Fatal("a syntax error was ignored")
	}
}

func TestAMissingRootStopsTheRun(t *testing.T) {
	if _, err := check(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a missing root was ignored")
	}
}

func TestRunSaysWhatItFoundAndHowToExplainIt(t *testing.T) {
	bad := source("func TestA(t *testing.T) {\n\tfor _, in := range []string{\"a\"} {\n\t\tt.Log(in)\n\t}\n}")
	for name, tc := range map[string]struct {
		files  map[string]string
		args   func(root string) []string
		code   int
		stdout string
		stderr string
	}{
		"clean": {map[string]string{"a_test.go": source("func TestA(t *testing.T) {}")},
			func(root string) []string { return []string{root} }, exitOK, "", ""},
		"the working directory by default": {map[string]string{"a_test.go": source("func TestA(t *testing.T) {}")},
			func(string) []string { return nil }, exitOK, "", ""},
		"reported": {map[string]string{"a_test.go": bad},
			func(root string) []string { return []string{root} }, exitReported, "a_test.go:6: TestA ranges over its cases without t.Run (run each case as a subtest, or explain with a `// subtests: <reason>` comment)\n", ""},
		"too many arguments": {nil,
			func(root string) []string { return []string{root, root} }, exitUsage, "", usage},
		"a broken file": {map[string]string{"a_test.go": "package p\n\nfunc {"},
			func(root string) []string { return []string{root} }, exitUsage, "", "subtests: "},
	} {
		t.Run(name, func(t *testing.T) {
			root := write(t, tc.files)
			if tc.args(root) == nil {
				t.Chdir(root)
			}
			var stdout, stderr bytes.Buffer
			code := run(tc.args(root), &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit %d, want %d (stderr %q)", code, tc.code, stderr.String())
			}
			if tc.stdout != "" && !strings.HasSuffix(stdout.String(), tc.stdout) {
				t.Errorf("stdout %q, want it to end with %q", stdout.String(), tc.stdout)
			}
			if tc.stdout == "" && stdout.Len() != 0 {
				t.Errorf("stdout %q, want nothing", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), tc.stderr) || (tc.stderr == "" && stderr.Len() != 0) {
				t.Errorf("stderr %q, want it to start with %q", stderr.String(), tc.stderr)
			}
		})
	}
}
