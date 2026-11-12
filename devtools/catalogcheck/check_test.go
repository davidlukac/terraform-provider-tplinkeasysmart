package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// entryText is one well-formed entry whose Automation field is the argument.
func entryText(id, automation string) string {
	return "<a id=\"" + strings.ToLower(id) + "\"></a>\n### " + id + " A title\n\n" +
		"**Rule.** The rule.\n\n**Why.** What happened.\n\n**Design question.** Ask this.\n\n" +
		"**Test requirement.** Test that.\n\n**Automation.** " + automation + "\n\n**Adversarial probe.** Try this.\n\n"
}

// catalogText is a catalog with two entries (DC-C01 and DC-L01) and a routing table that names both.
func catalogText(auto1, auto2 string) string {
	return "# Defect classes\n\n## Routing: which entries does my change touch?\n\n| My change | Read |\n|---|---|\n" +
		"| Any change | [DC-C01](#dc-c01), [DC-L01](#dc-l01) |\n\n---\n\n## Common classes\n\n" +
		entryText("DC-C01", auto1) + "---\n\n## Library classes\n\n" + entryText("DC-L01", auto2)
}

// goodAutomation names one check of every kind, all of which exist in baseRepo.
const goodAutomation = "`test:TestNamed`, `fuzz:FuzzNamed`, `make:soak`, `lint:gocritic`, `analyzer:mytool`, `analyzer:myanalyzer`, " +
	"`script:scripts/run.sh`, `workflow:test.yml`; `proposed:later(#12)` (not built); `reading:` whether it is meant needs a person."

// baseRepo is a small repository in which everything the catalog names exists.
func baseRepo() map[string]string {
	return map[string]string{
		"docs/defect-classes.md":     catalogText(goodAutomation, "`test:TestSecond`"),
		"named_test.go":              "package foo\n\nimport \"testing\"\n\nfunc TestNamed(t *testing.T) {}\n",
		"other_test.go":              "package foo\n\nimport \"testing\"\n\nfunc TestSecond(t *testing.T) {}\n\nfunc FuzzNamed(f *testing.F) {}\n",
		"Makefile":                   "DEVTOOL := go tool\n.PHONY: lint soak\n\nlint:\n\techo lint\n\nsoak:\n\techo soak\n",
		".golangci.yml":              "version: \"2\"\nlinters:\n  default: none\n  enable:\n    - errcheck\n    - gocritic # a comment\n  settings:\n    gocritic: {}\n",
		"devtools/mytool/m.go":       "package main\n\nvar a = &Analyzer{\n\tName: \"myanalyzer\",\n}\n",
		"scripts/run.sh":             "#!/bin/sh\n",
		".github/workflows/test.yml": "name: test\n",
	}
}

// writeRepo writes the files under a new directory and returns it.
func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func replaceIn(files map[string]string, file, old, repl string) {
	if !strings.Contains(files[file], old) {
		panic("the fixture has no " + old + " in " + file)
	}
	files[file] = strings.Replace(files[file], old, repl, 1)
}

func TestTheCatalogCheck(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(files map[string]string)
		// want holds one text per problem the check must report, each contained in a different problem; empty means no problem at all.
		want []string
	}{
		{name: "everything the catalog names exists", mutate: func(map[string]string) {}},

		// The names of the checks.
		{name: "a renamed test is reported", want: []string{"DC-C01: `test:TestNamed` does not resolve: no function of that name in a _test.go file (a renamed or removed test?)"},
			mutate: func(f map[string]string) { replaceIn(f, "named_test.go", "TestNamed", "TestRenamed") }},
		{name: "a test that exists only in a file that is not a test file does not resolve", want: []string{"`test:TestNamed` does not resolve"},
			mutate: func(f map[string]string) { f["named.go"] = f["named_test.go"]; delete(f, "named_test.go") }},
		{name: "a test in a testdata fixture does not resolve", want: []string{"`test:TestNamed` does not resolve"},
			mutate: func(f map[string]string) { f["testdata/x_test.go"] = f["named_test.go"]; delete(f, "named_test.go") }},
		{name: "a method named like a test does not resolve", want: []string{"`test:TestNamed` does not resolve"},
			mutate: func(f map[string]string) {
				f["named_test.go"] = "package foo\n\ntype s struct{}\n\nfunc (s) TestNamed() {}\n"
			}},
		{name: "a missing fuzz target is reported", want: []string{"`fuzz:FuzzNamed` does not resolve"},
			mutate: func(f map[string]string) { replaceIn(f, "other_test.go", "FuzzNamed", "FuzzOther") }},
		{name: "a removed make target is reported", want: []string{"`make:soak` does not resolve: no such target in the Makefile"},
			mutate: func(f map[string]string) { replaceIn(f, "Makefile", "soak:", "soaked:") }},
		{name: "a make variable is not a target", want: []string{"`make:DEVTOOL` does not resolve"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "`make:soak`", "`make:DEVTOOL`") }},
		{name: ".PHONY is not a target", want: []string{"`make:.PHONY` does not resolve"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "`make:soak`", "`make:.PHONY`") }},
		{name: "a linter that is configured but not enabled is reported", want: []string{"`lint:gocritic` does not resolve: not enabled in .golangci.yml"},
			mutate: func(f map[string]string) { replaceIn(f, ".golangci.yml", "    - gocritic # a comment\n", "") }},
		{name: "without a linter configuration a lint name is reported", want: []string{"`lint:gocritic` does not resolve: the .golangci.yml has no `linters: enable:` list"},
			mutate: func(f map[string]string) { delete(f, ".golangci.yml") }},
		{name: "a missing analyzer directory is reported", want: []string{"`analyzer:mytool` does not resolve"},
			mutate: func(f map[string]string) {
				f["devtools/other/m.go"] = f["devtools/mytool/m.go"]
				delete(f, "devtools/mytool/m.go")
			}},
		{name: "an analyzer is found by the name it declares", want: []string{"`analyzer:myanalyzer` does not resolve"},
			mutate: func(f map[string]string) { replaceIn(f, "devtools/mytool/m.go", "myanalyzer", "renamed") }},
		{name: "a missing script is reported", want: []string{"`script:scripts/run.sh` does not resolve"},
			mutate: func(f map[string]string) { delete(f, "scripts/run.sh") }},
		{name: "a missing workflow is reported", want: []string{"`workflow:test.yml` does not resolve: no such file in .github/workflows"},
			mutate: func(f map[string]string) { delete(f, ".github/workflows/test.yml") }},
		{name: "a name in the second entry is checked too", want: []string{"DC-L01: `test:TestGone` does not resolve"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`test:TestSecond`\n", "`test:TestGone`\n")
			}},

		// The form of a token.
		{name: "a name that is not a Test function is reported", want: []string{"`test:helper` is not a Test function name"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`test:TestNamed`,", "`test:helper`,")
			}},
		{name: "an unknown kind is reported", want: []string{`unknown kind "tset"`},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`test:TestNamed`,", "`tset:TestNamed`,")
			}},
		{name: "a token without a name is reported", want: []string{"`make:` needs a name"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "`make:soak`", "`make:`") }},
		{name: "reading without a reason is reported", want: []string{"`reading:` carries no reason"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`reading:` whether it is meant needs a person.", "`reading:` it does.")
			}},
		{name: "reading with nothing after it is reported", want: []string{"`reading:` carries no reason"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`reading:` whether it is meant needs a person.", "`reading:`")
			}},
		{name: "a reason that holds a code span is a reason", mutate: func(f map[string]string) {
			replaceIn(f, "docs/defect-classes.md", "`reading:` whether it is meant needs a person.", "`reading:` whether a `sent` flag is set on every return needs a person.")
		}},
		{name: "a reason ends where the next token starts", want: []string{"`reading:` carries no reason"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`reading:` whether it is meant needs a person.", "`reading:` due; `make:soak`; and then some words")
			}},
		{name: "proposed without an issue is reported", want: []string{"`proposed:later` must be `proposed:<name>(#<issue>)`"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`proposed:later(#12)`", "`proposed:later`")
			}},
		{name: "proposed with a text instead of a number is reported", want: []string{"`proposed:later(#soon)` must be"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "later(#12)", "later(#soon)") }},
		{name: "a proposal whose test exists is out of date", want: []string{"`proposed:TestNamed(#12)` is built already"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "later(#12)", "TestNamed(#12)") }},
		{name: "a proposal whose analyzer exists is out of date", want: []string{"`proposed:mytool(#12)` is built already"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "later(#12)", "mytool(#12)") }},
		{name: "a proposal whose make target exists is out of date", want: []string{"`proposed:soak(#12)` is built already"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "later(#12)", "soak(#12)") }},
		{name: "an automation field without a token is reported", want: []string{"DC-L01: Automation names no check"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "`test:TestSecond`\n", "a test exists.\n")
			}},

		// The shape of an entry.
		{name: "a missing field is reported", want: []string{`DC-L01: missing field "Why"`},
			mutate: func(f map[string]string) {
				head, tail, _ := strings.Cut(f["docs/defect-classes.md"], "## Library classes")
				f["docs/defect-classes.md"] = head + "## Library classes" + strings.Replace(tail, "**Why.** What happened.\n\n", "", 1)
			}},
		{name: "a field out of order is reported", want: []string{`DC-C01: field "Rule" is out of order`},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "**Rule.** The rule.\n\n**Why.** What happened.\n\n", "**Why.** What happened.\n\n**Rule.** The rule.\n\n")
			}},
		{name: "an empty field is reported", want: []string{`DC-C01: field "Why" is empty`},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "**Why.** What happened.", "**Why.**")
			}},
		{name: "a field that runs over several lines is not empty", mutate: func(f map[string]string) {
			replaceIn(f, "docs/defect-classes.md", "**Why.** What happened.", "**Why.**\nIt happened on\nthree lines.")
		}},
		{name: "a repeated field is reported", want: []string{`DC-C01: field "Rule" appears twice`},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "**Why.**", "**Rule.** Again.\n\n**Why.**")
			}},
		{name: "an unknown field is reported", want: []string{`DC-C01: unknown field "Notes"`},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "**Why.**", "**Notes.** Hm.\n\n**Why.**")
			}},

		// Anchors and the routing table.
		{name: "an anchor that is not the id of its heading is reported", want: []string{`anchor "dc-c02" does not match the heading DC-C01`, "the link #dc-c01 goes to no anchor"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "<a id=\"dc-c01\">", "<a id=\"dc-c02\">")
			}},
		{name: "a heading without an anchor is reported", want: []string{"DC-C01 has no anchor line", "the link #dc-c01 goes to no anchor"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", "<a id=\"dc-c01\"></a>\n", "") }},
		{name: "an anchor without a heading is reported", want: []string{`anchor "dc-c09" is not followed by an entry heading`},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "---\n\n## Library", "<a id=\"dc-c09\"></a>\n---\n\n## Library")
			}},
		{name: "a malformed entry heading is reported", want: []string{"entry heading must be", `anchor "dc-c01" is not followed by an entry heading`, "DC-C01 is not an entry of"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "### DC-C01 A title", "### DC-C1 A title")
			}},
		{name: "an entry that is in no row of the routing table is reported", want: []string{"DC-L01 is in no row of the routing table"},
			mutate: func(f map[string]string) { replaceIn(f, "docs/defect-classes.md", ", [DC-L01](#dc-l01)", "") }},
		{name: "a routed id that is not an entry is reported", want: []string{"DC-L07 is not an entry of", "the link #dc-l07 goes to no anchor"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", ", [DC-L01](#dc-l01)", ", [DC-L01](#dc-l01), [DC-L07](#dc-l07)")
			}},
		{name: "a routing link to another anchor is reported", want: []string{"the routing link of DC-L01 goes to #dc-c01, not #dc-l01"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "[DC-L01](#dc-l01)", "[DC-L01](#dc-c01)")
			}},
		{name: "a link inside the page to no anchor is reported", want: []string{"the link #nowhere goes to no anchor"},
			mutate: func(f map[string]string) {
				replaceIn(f, "docs/defect-classes.md", "**Rule.** The rule.", "**Rule.** See [here](#nowhere).")
			}},
		{name: "a link to a heading of the page resolves", mutate: func(f map[string]string) {
			replaceIn(f, "docs/defect-classes.md", "**Rule.** The rule.", "**Rule.** See [the table](#routing-which-entries-does-my-change-touch).")
		}},

		// The ids and links used elsewhere.
		{name: "an id used in another file resolves", mutate: func(f map[string]string) {
			f["README.md"] = "See [DC-L01](docs/defect-classes.md#dc-l01) and DC-C01.\n"
			f["foo.go"] = "package foo\n\n// DC-C01: the rule.\n"
		}},
		{name: "an id used in a document that is not an entry is reported", want: []string{"README.md:2: DC-L99 is not an entry"},
			mutate: func(f map[string]string) { f["README.md"] = "Text.\nSee DC-L99.\n" }},
		{name: "an id used in a comment of a Go file that is not an entry is reported", want: []string{"foo.go:3: DC-C77 is not an entry"},
			mutate: func(f map[string]string) { f["foo.go"] = "package foo\n\n// DC-C77 is gone.\n" }},
		{name: "a link to an anchor that is not there is reported", want: []string{"README.md:1: the link defect-classes.md#dc-c05 goes to no anchor"},
			mutate: func(f map[string]string) { f["README.md"] = "[x](docs/defect-classes.md#dc-c05)\n" }},
		{name: "an id in a fixture under devtools or testdata is not read", mutate: func(f map[string]string) {
			f["devtools/mytool/testdata/a.md"] = "DC-L99\n"
			f["testdata/a.md"] = "DC-L99\n"
			f["devtools/mytool/m_test.go"] = "package main\n\n// DC-L99\n"
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := baseRepo()
			tc.mutate(files)
			problems, err := Check(writeRepo(t, files), DefaultOptions())
			if err != nil {
				t.Fatal(err)
			}
			// Each wanted text names one problem, and there is no problem that no text names.
			used := make([]bool, len(problems))
			for _, w := range tc.want {
				found := false
				for i, p := range problems {
					if !used[i] && strings.Contains(p.String(), w) {
						used[i], found = true, true
						break
					}
				}
				if !found {
					t.Errorf("no problem contains %q; problems: %v", w, problems)
				}
			}
			for i, p := range problems {
				if !used[i] {
					t.Errorf("unexpected problem: %v", p)
				}
			}
		})
	}
}

// A problem names its file and its line, and the problems come out in file and line order.
func TestProblemsAreSortedAndNameTheirLine(t *testing.T) {
	files := baseRepo()
	files["README.md"] = "DC-L90\nDC-L91\n"
	replaceIn(files, ".golangci.yml", "    - gocritic # a comment\n", "")
	problems, err := Check(writeRepo(t, files), DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range problems {
		got = append(got, p.File+":"+strconv.Itoa(p.Line))
	}
	want := []string{"README.md:1", "README.md:2"}
	if len(problems) != 3 || strings.Join(got[:2], ",") != strings.Join(want, ",") || problems[2].File != defaultCatalog {
		t.Fatalf("problems = %v", problems)
	}
	if !strings.HasPrefix(problems[2].String(), defaultCatalog+":") {
		t.Errorf("String() = %q", problems[2])
	}
}

func TestMissingCatalogIsAnError(t *testing.T) {
	if _, err := Check(t.TempDir(), DefaultOptions()); err == nil || !strings.Contains(err.Error(), "reading the catalog") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnabledLinters(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   []string
		ok     bool
	}{
		{name: "the enable list", config: "linters:\n  default: none\n  enable:\n    - a\n    - b-c # note\n\n    # c\n    - d_e\n  settings:\n    x: {}\n", want: []string{"a", "b-c", "d_e"}, ok: true},
		{name: "a list of another key is not the enable list", config: "linters:\n  disable:\n    - a\n", ok: false},
		{name: "a list outside linters is not read", config: "formatters:\n  enable:\n    - gofmt\nlinters:\n  enable:\n    - a\n", want: []string{"a"}, ok: true},
		{name: "the list ends where its section does", config: "linters:\n  enable:\n    - a\nrun:\n  enable:\n    - b\n", want: []string{"a"}, ok: true},
		{name: "an empty file", config: "", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := enabledLinters(tc.config)
			if ok != tc.ok || len(got) != len(tc.want) {
				t.Fatalf("got %v, %v; want %v, %v", got, ok, tc.want, tc.ok)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("%q is missing from %v", w, got)
				}
			}
		})
	}
}

func TestMakeTargetPattern(t *testing.T) {
	tests := []struct {
		line string
		want string // the names, space-separated; empty when the line is not a rule
	}{
		{"lint: spell", "lint"},
		{"lint:", "lint"},
		{"a b : c", "a b"},
		{"mutation-diff:", "mutation-diff"},
		{"DEVTOOL := go tool", ""},
		{"X ?= y", ""},
		{".PHONY: lint", ""},
		{"\tgo test ./...", ""},
		{"# lint: not a rule", ""},
	}
	for _, tc := range tests {
		t.Run(tc.line, func(t *testing.T) {
			got := ""
			if m := makeTarget.FindStringSubmatch(tc.line); m != nil {
				got = m[1]
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ heading, want string }{
		{"## Routing: which entries does my change touch?", "routing-which-entries-does-my-change-touch"},
		{"### DC-C01 Every documented guarantee has a test", "dc-c01-every-documented-guarantee-has-a-test"},
		{"## Entry format", "entry-format"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			if got := slug(tc.heading); got != tc.want {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestTheCommandPrintsOneLinePerProblemAndSetsTheStatus(t *testing.T) {
	good := writeRepo(t, baseRepo())
	bad := baseRepo()
	replaceIn(bad, "named_test.go", "TestNamed", "TestRenamed")
	replaceIn(bad, "Makefile", "soak:", "soaked:")
	tests := []struct {
		name       string
		args       []string
		wantStatus int
		wantOut    int // lines on stdout
		wantErr    string
	}{
		{name: "a good catalog", args: []string{"-root", good}, wantStatus: 0},
		{name: "two problems", args: []string{"-root", writeRepo(t, bad)}, wantStatus: 1, wantOut: 2, wantErr: "2 problem(s)"},
		{name: "no catalog", args: []string{"-root", t.TempDir()}, wantStatus: 2, wantErr: "reading the catalog"},
		{name: "a bad flag", args: []string{"-nope"}, wantStatus: 2, wantErr: "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(tc.args, &out, &errOut); got != tc.wantStatus {
				t.Fatalf("status = %d, want %d (stderr %q)", got, tc.wantStatus, errOut.String())
			}
			if n := strings.Count(out.String(), "\n"); n != tc.wantOut {
				t.Errorf("stdout has %d lines, want %d: %q", n, tc.wantOut, out.String())
			}
			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}
		})
	}
}
