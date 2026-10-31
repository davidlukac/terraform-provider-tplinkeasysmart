package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const module = "example.com/m"

// profileOf builds a cover profile: one line per block {file, startLine, endLine, statements, hits}.
func profileOf(blocks ...[5]any) string {
	var b strings.Builder
	b.WriteString("mode: atomic\n")
	for _, x := range blocks {
		b.WriteString(module + "/" + x[0].(string) + ":" + itoa(x[1]) + ".1," + itoa(x[2]) + ".2 " + itoa(x[3]) + " " + itoa(x[4]) + "\n")
	}
	return b.String()
}

func itoa(v any) string { return strconv.Itoa(v.(int)) }

// project writes a module with the given files into a temporary directory and returns its root.
func project(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module " + module + "\n\ngo 1.22\n"
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func mustParse(t *testing.T, text string) map[span]counts {
	t.Helper()
	blocks, err := parseProfile(text, module)
	if err != nil {
		t.Fatal(err)
	}
	return blocks
}

func TestDuplicateBlocksFromSeveralTestBinariesAreMerged(t *testing.T) {
	blocks := mustParse(t, profileOf([5]any{"a.go", 1, 2, 3, 0}, [5]any{"a.go", 1, 2, 3, 5}))
	if got := blocks[span{"a.go", 1, 2, 1, 2}]; got != (counts{statements: 3, hits: 5}) {
		t.Fatalf("got %+v, want 3 statements and 5 hits", got)
	}
}

func TestTestSupportPackagesAreNotMeasured(t *testing.T) {
	blocks := mustParse(t, profileOf([5]any{"internal/escp/escptest/switch.go", 1, 2, 9, 0}, [5]any{"internal/escp/client.go", 1, 2, 3, 1}))
	want := map[span]counts{{"internal/escp/client.go", 1, 2, 1, 2}: {3, 1}}
	if !reflect.DeepEqual(blocks, want) {
		t.Fatalf("got %v, want %v", blocks, want)
	}
}

func TestAPackageThatOnlyLooksLikeTestSupportIsMeasured(t *testing.T) {
	blocks := mustParse(t, profileOf([5]any{"internal/escp/escptestx/a.go", 1, 2, 3, 0}))
	if _, ok := blocks[span{"internal/escp/escptestx/a.go", 1, 2, 1, 2}]; len(blocks) != 1 || !ok {
		t.Fatalf("got %v, want the block of escptestx", blocks)
	}
}

func TestAMalformedProfileIsAnError(t *testing.T) {
	if _, err := parseProfile("mode: atomic\ngarbage\n", module); err == nil || !strings.Contains(err.Error(), "garbage") {
		t.Fatalf("got %v, want an error naming the line", err)
	}
}

func TestModuleOf(t *testing.T) {
	root := project(t, map[string]string{})
	if got, err := moduleOf(root); err != nil || got != module {
		t.Fatalf("got %q, %v; want %q", got, err, module)
	}
	if _, err := moduleOf(t.TempDir()); err == nil {
		t.Fatal("a directory without go.mod must be an error")
	}
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "go.mod"), []byte("go 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := moduleOf(empty); err == nil || !strings.Contains(err.Error(), "no module line") {
		t.Fatalf("got %v, want an error about the missing module line", err)
	}
}

func TestFullyCoveredCodePasses(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n"})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 1, 2, 4, 1})), root)
	if r.total != 4 || r.covered != 4 || r.excluded != 0 || len(r.uncovered) != 0 || len(r.problems) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestUncoveredCodeIsReportedWithItsLocation(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n"})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 7, 9, 2, 0})), root)
	want := []uncoveredBlock{{span: span{"a.go", 7, 9, 1, 2}, statements: 2}}
	if !reflect.DeepEqual(r.uncovered, want) {
		t.Fatalf("got %v, want %v", r.uncovered, want)
	}
}

const markedSource = `package a

// main is the entry point.
// coverage: ignore - process entry point
func main() {
	run()
}

func other() {
	x()
}
`

func TestAMarkedFunctionExcludesOnlyItsOwnStatements(t *testing.T) {
	root := project(t, map[string]string{"a.go": markedSource})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 6, 6, 1, 0}, [5]any{"a.go", 10, 10, 1, 0})), root)
	if r.covered != 0 || r.excluded != 1 || len(r.problems) != 0 {
		t.Fatalf("got %+v", r)
	}
	if want := []uncoveredBlock{{span: span{"a.go", 10, 10, 1, 2}, statements: 1}}; !reflect.DeepEqual(r.uncovered, want) {
		t.Fatalf("the unmarked function must stay uncovered: got %v", r.uncovered)
	}
	if want := []exclusionTotal{{file: "a.go", line: 5, reason: "process entry point", statements: 1}}; !reflect.DeepEqual(r.exclusions, want) {
		t.Fatalf("got %v, want %v", r.exclusions, want)
	}
}

func TestGapsAreListedByFileThenLine(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n", "b.go": "package b\n"})
	r := evaluate(mustParse(t, profileOf(
		[5]any{"b.go", 2, 2, 1, 0}, [5]any{"a.go", 9, 9, 1, 0}, [5]any{"a.go", 5, 6, 1, 0}, [5]any{"a.go", 5, 5, 1, 0}, [5]any{"a.go", 3, 3, 1, 0})), root)
	var got []span
	for _, u := range r.uncovered {
		got = append(got, u.span)
	}
	want := []span{{"a.go", 3, 3, 1, 2}, {"a.go", 5, 5, 1, 2}, {"a.go", 5, 6, 1, 2}, {"a.go", 9, 9, 1, 2}, {"b.go", 2, 2, 1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Two blocks on the same lines are two blocks: the profile tells them apart by their columns, and
// one block's hits must not hide the other's gap.
func TestBlocksOnTheSameLinesAreToldApartByTheirColumns(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n"})
	text := "mode: atomic\n" +
		module + "/a.go:5.30,5.40 1 0\n" +
		module + "/a.go:5.10,5.20 2 3\n" +
		module + "/a.go:5.10,5.25 1 0\n"
	r := evaluate(mustParse(t, text), root)
	if r.total != 4 || r.covered != 2 {
		t.Fatalf("got %+v, want 4 statements and 2 covered", r)
	}
	var got []span
	for _, u := range r.uncovered {
		got = append(got, u.span)
	}
	want := []span{{"a.go", 5, 5, 10, 25}, {"a.go", 5, 5, 30, 40}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSeveralBlocksOfOneExcludedFunctionAreTotalled(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n\n// coverage: ignore - needs a real network\nfunc f() {\n\tif x {\n\t\ty()\n\t}\n\tz()\n}\n"})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 5, 7, 2, 0}, [5]any{"a.go", 8, 8, 1, 0})), root)
	want := []exclusionTotal{{file: "a.go", line: 4, reason: "needs a real network", statements: 3}}
	if r.excluded != 3 || !reflect.DeepEqual(r.exclusions, want) {
		t.Fatalf("got %+v", r)
	}
}

func TestAMarkerOnAMethodCountsToo(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n\ntype T struct{}\n\n// Close does nothing useful here.\n// coverage: ignore - a thin wrapper\nfunc (T) Close() {\n\tz()\n}\n"})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 8, 8, 1, 0})), root)
	if r.excluded != 1 || len(r.uncovered) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestAMarkerWithoutAReasonIsAProblem(t *testing.T) {
	for name, comment := range map[string]string{"no dash": "// coverage: ignore", "a dash and no text": "// coverage: ignore -", "text without a dash": "// coverage: ignore because I said so"} {
		t.Run(name, func(t *testing.T) {
			root := project(t, map[string]string{"a.go": "package a\n\n" + comment + "\nfunc f() {\n\tx()\n}\n"})
			r := evaluate(mustParse(t, profileOf([5]any{"a.go", 5, 5, 1, 0})), root)
			if len(r.problems) != 1 || !strings.Contains(r.problems[0], "a.go:3: 'coverage: ignore' needs a reason") {
				t.Fatalf("got %v, want one problem at line 3", r.problems)
			}
		})
	}
}

func TestOnlyADocCommentOfAFunctionMarks(t *testing.T) {
	src := "package a\n\n// coverage: ignore - this is a doc of a variable\nvar v = 1\n\n// f has a doc comment without the marker.\nfunc f() {\n\t// coverage: ignore - inside the body\n\tx()\n}\n\nfunc g() {\n\ty()\n}\n"
	root := project(t, map[string]string{"a.go": src})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 9, 9, 1, 0}, [5]any{"a.go", 13, 13, 1, 0})), root)
	if r.excluded != 0 || len(r.uncovered) != 2 || len(r.problems) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestAMissingSourceFileLeavesBlocksUncovered(t *testing.T) {
	r := evaluate(mustParse(t, profileOf([5]any{"gone.go", 1, 2, 1, 0})), project(t, map[string]string{}))
	if len(r.uncovered) != 1 || len(r.problems) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestASourceFileThatDoesNotParseIsAProblem(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\nfunc {\n"})
	r := evaluate(mustParse(t, profileOf([5]any{"a.go", 1, 2, 1, 0})), root)
	if len(r.problems) != 1 || !strings.Contains(r.problems[0], "cannot read the 'coverage: ignore' markers") {
		t.Fatalf("got %v", r.problems)
	}
}

func TestTheReportListsExclusionsAndGaps(t *testing.T) {
	root := project(t, map[string]string{"a.go": markedSource, "b.go": "package b\n\n// coverage: ignore\nfunc f() {\n\tx()\n}\n"})
	var out bytes.Buffer
	code := writeReport(&out, evaluate(mustParse(t, profileOf(
		[5]any{"a.go", 6, 6, 1, 0}, [5]any{"a.go", 10, 10, 1, 0}, [5]any{"a.go", 1, 1, 8, 1}, [5]any{"b.go", 5, 5, 1, 0})), root))
	// b.go's marker has no reason: its function is still excluded, and the marker is a problem of its own
	want := "statements: 11  covered: 8  excluded: 2  uncovered: 1\n" +
		"effective coverage (excluded statements not counted): 88.9%\n" +
		"  excluded 1 statement(s) in a.go:5 - process entry point\n" +
		"  excluded 1 statement(s) in b.go:4 - \n" +
		"  UNCOVERED a.go:10-10 (1 statement(s))\n" +
		"  PROBLEM " + filepath.Join(root, "b.go") + ":3: 'coverage: ignore' needs a reason: // coverage: ignore - <reason>\n"
	if out.String() != want || code != exitViolated {
		t.Fatalf("exit %d, output:\n%s\nwant:\n%s", code, out.String(), want)
	}
}

func TestAnEmptyProfileMeetsThePolicy(t *testing.T) {
	var out bytes.Buffer
	if code := writeReport(&out, evaluate(mustParse(t, "mode: atomic\n"), project(t, map[string]string{}))); code != exitOK || !strings.Contains(out.String(), "100.0%") {
		t.Fatalf("exit %d, output %q", code, out.String())
	}
}

func TestBadgeCountsCoveredOverAllStatements(t *testing.T) {
	var out bytes.Buffer
	writeBadge(&out, mustParse(t, profileOf([5]any{"a.go", 1, 2, 9, 1}, [5]any{"a.go", 3, 4, 1, 0})))
	want := `{"schemaVersion":1,"label":"coverage","message":"90.0%","color":"green"}` + "\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
}

func TestBadgeMergesDuplicateBlocks(t *testing.T) {
	var out bytes.Buffer
	writeBadge(&out, mustParse(t, profileOf([5]any{"a.go", 1, 2, 4, 0}, [5]any{"a.go", 1, 2, 4, 3})))
	if !strings.Contains(out.String(), `"message":"100.0%"`) {
		t.Fatalf("got %q", out.String())
	}
}

func TestBadgeDoesNotForgiveExclusions(t *testing.T) {
	// the policy would exclude the uncovered block through `coverage: ignore`; the badge must not
	var out bytes.Buffer
	writeBadge(&out, mustParse(t, profileOf([5]any{"a.go", 1, 2, 99, 1}, [5]any{"a.go", 5, 6, 1, 0})))
	if !strings.Contains(out.String(), `"message":"99.0%"`) {
		t.Fatalf("got %q", out.String())
	}
}

func TestBadgeOfAnEmptyProfileIsZeroNotADivisionError(t *testing.T) {
	var out bytes.Buffer
	writeBadge(&out, mustParse(t, "mode: atomic\n"))
	if !strings.Contains(out.String(), `"message":"0.0%"`) || !strings.Contains(out.String(), `"color":"red"`) {
		t.Fatalf("got %q", out.String())
	}
}

func TestColourBands(t *testing.T) {
	for pct, want := range map[float64]string{99: "brightgreen", 95: "brightgreen", 94.9: "green", 90: "green", 80: "yellowgreen", 70: "yellow", 60: "orange", 59.9: "red"} {
		if got := colour(pct); got != want {
			t.Errorf("colour(%v) = %s, want %s", pct, got, want)
		}
	}
}

func TestRun(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n"})
	write := func(name, text string) string {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good.out", profileOf([5]any{"a.go", 1, 2, 4, 1}))
	bad := write("bad.out", profileOf([5]any{"a.go", 1, 2, 4, 0}))
	garbled := write("garbled.out", "mode: atomic\nnot a block\n")
	for _, c := range []struct {
		name   string
		args   []string
		code   int
		stdout string // a fragment
		stderr string // a fragment
	}{
		{"check passes", []string{"check", good, root}, exitOK, "effective coverage", ""},
		{"check fails on a gap", []string{"check", bad, root}, exitViolated, "UNCOVERED a.go:1-2", ""},
		{"badge prints the json", []string{"badge", good, root}, exitOK, `"message":"100.0%"`, ""},
		{"no arguments explain the usage", nil, exitUsage, "", "usage: coverpolicy"},
		{"an unknown command explains the usage", []string{"frobnicate", good}, exitUsage, "", "usage: coverpolicy"},
		{"a missing profile", []string{"check", filepath.Join(root, "nope.out"), root}, exitUsage, "", "no such file"},
		{"a directory without go.mod", []string{"check", good, t.TempDir()}, exitUsage, "", "reading the module path"},
		{"an unparseable profile", []string{"check", garbled, root}, exitUsage, "", "unparseable cover profile line"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(c.args, &stdout, &stderr); code != c.code {
				t.Fatalf("exit code %d, want %d (stdout %q, stderr %q)", code, c.code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), c.stdout) || !strings.Contains(stderr.String(), c.stderr) {
				t.Fatalf("stdout %q, stderr %q; want fragments %q and %q", stdout.String(), stderr.String(), c.stdout, c.stderr)
			}
		})
	}
}

func TestRunDefaultsTheRootToTheCurrentDirectory(t *testing.T) {
	root := project(t, map[string]string{"a.go": "package a\n"})
	profile := filepath.Join(root, "cover.out")
	if err := os.WriteFile(profile, []byte(profileOf([5]any{"a.go", 1, 2, 4, 1})), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"check", profile}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}
