package main

import (
	"bytes"
	"strings"
	"testing"
)

const (
	providerCatalog = "contributing/defect-classes.md"
	libraryURL      = "https://example.org/lib/blob/main/docs/defect-classes.md"
)

// providerOptions are the options of a repository whose catalog defines DC-P entries.
func providerOptions() Options { return Options{Catalog: providerCatalog, Letters: "P"} }

// providerCatalogText is a catalog in the layout of a second repository: its own entry (DC-P01), the common
// classes of another repository's catalog linked by absolute URL, and a table that says how they apply.
func providerCatalogText(auto string) string {
	return "# Defect classes of the provider\n\nThe common classes are in the [library's catalog](" + libraryURL + ").\n\n" +
		"## Routing: which entries does my change touch?\n\n| My change | Read |\n|---|---|\n" +
		"| Any change | [DC-C01](" + libraryURL + "#dc-c01), [DC-C05](" + libraryURL + "#dc-c05) |\n" +
		"| A resource | [DC-P01](#dc-p01) |\n\n---\n\n" +
		"## How the common classes apply here\n\n| Class | In this repository |\n|---|---|\n" +
		"| [DC-C01](" + libraryURL + "#dc-c01) guarantees | `test:TestProviderThing`, `make:testacc`; `proposed:canary(#7)`. |\n" +
		"| [DC-C05](" + libraryURL + "#dc-c05) tests | `make:testacc`; `reading:` whether it is meant needs a person. |\n\n---\n\n" +
		"## Provider classes\n\n" + entryText("DC-P01", auto)
}

// providerRepo is a repository with its tests below internal/ and its catalog below contributing/, in which everything resolves.
func providerRepo() map[string]string {
	return map[string]string{
		providerCatalog:                   providerCatalogText("`test:TestProviderThing`, `make:testacc`, `lint:gocritic`"),
		"internal/provider/thing_test.go": "package provider\n\nimport \"testing\"\n\nfunc TestProviderThing(t *testing.T) {}\n",
		"Makefile":                        "testacc:\n\techo acc\n",
		".golangci.yml":                   "linters:\n  enable:\n    - gocritic\n",
	}
}

func TestCheckAcceptsAnotherCatalogLayout(t *testing.T) {
	tests := []struct {
		name   string
		opts   Options
		mutate func(files map[string]string)
		want   []string // one text per problem, each contained in a different problem
	}{
		{name: "a second layout with its own flags is clean", opts: providerOptions(), mutate: func(map[string]string) {}},
		{name: "a test below internal is found without a flag", opts: providerOptions(),
			want: []string{"contributing/defect-classes.md:18: `test:TestProviderThing` does not resolve", "contributing/defect-classes.md:36: DC-P01: `test:TestProviderThing` does not resolve"},
			mutate: func(f map[string]string) {
				replaceIn(f, "internal/provider/thing_test.go", "TestProviderThing", "TestRenamed")
			}},
		{name: "an entry with a letter of another catalog is reported", opts: providerOptions(),
			want: []string{"entry heading must be `### DC-<P><two digits> <title>`", `anchor "dc-c09" is not followed by an entry heading`},
			mutate: func(f map[string]string) {
				replaceIn(f, providerCatalog, "## Provider classes\n\n", "## Provider classes\n\n"+entryText("DC-C09", "`make:testacc`"))
			}},
		{name: "a letter the page lists makes its ids entries", opts: Options{Catalog: providerCatalog, Letters: "C,P"},
			want:   []string{"9: DC-C01 is not an entry", "9: DC-C05 is not an entry", "18: DC-C01 is not an entry", "19: DC-C05 is not an entry"},
			mutate: func(map[string]string) {}},

		// Ids and links of the other catalog: the shape only.
		{name: "an id of another letter used in a file is not checked", opts: providerOptions(), mutate: func(f map[string]string) {
			f["README.md"] = "See DC-C03 and DC-L08.\n"
			f["foo.go"] = "package foo\n\n// DC-C77\n"
		}},
		{name: "an id of the page's letter that is not an entry is reported", opts: providerOptions(), want: []string{"README.md:1: DC-P09 is not an entry of contributing/defect-classes.md"},
			mutate: func(f map[string]string) { f["README.md"] = "DC-P09\n" }},
		{name: "a link by URL to an id of the other page is accepted", opts: providerOptions(), mutate: func(f map[string]string) {
			f["README.md"] = "[x](" + libraryURL + "#dc-c06) [y](" + libraryURL + "#dc-c06-a-title) [z](" + libraryURL + "#routing-which-entries)\n"
		}},
		{name: "a link by URL to an anchor of the wrong shape is reported", opts: providerOptions(), want: []string{"README.md:1: the link " + libraryURL + "#DC-C06 has an anchor of the wrong shape"},
			mutate: func(f map[string]string) { f["README.md"] = "[x](" + libraryURL + "#DC-C06)\n" }},
		{name: "a link by URL to a malformed id is reported", opts: providerOptions(), want: []string{"#dc-c6 has an anchor of the wrong shape"},
			mutate: func(f map[string]string) { f["README.md"] = "[x](" + libraryURL + "#dc-c6)\n" }},
		{name: "a link by URL to an id with three digits is reported", opts: providerOptions(), want: []string{"#dc-c006 has an anchor of the wrong shape"},
			mutate: func(f map[string]string) { f["README.md"] = "[x](" + libraryURL + "#dc-c006)\n" }},
		{name: "a link to the page by a relative path is read against the page", opts: providerOptions(), want: []string{"README.md:2: the link defect-classes.md#dc-c01 goes to no anchor of contributing/defect-classes.md"},
			mutate: func(f map[string]string) {
				f["README.md"] = "[ok](contributing/defect-classes.md#dc-p01)\n[no](contributing/defect-classes.md#dc-c01)\n"
			}},

		// The tables of the page outside the entries.
		{name: "a name in a table row that does not resolve is reported", opts: providerOptions(),
			want: []string{"contributing/defect-classes.md:18: `make:testacc` does not resolve", "contributing/defect-classes.md:19: `make:testacc` does not resolve", "contributing/defect-classes.md:36: DC-P01: `make:testacc` does not resolve", "contributing/defect-classes.md:36: DC-P01: `make:testacc` does not resolve"},
			mutate: func(f map[string]string) {
				replaceIn(f, providerCatalog, "`lint:gocritic`", "`make:testacc`")
				f["Makefile"] = "other:\n\techo\n"
			}},
		{name: "a proposal and a reading of a table row are checked", opts: providerOptions(), want: []string{"`proposed:canary` must be", "`reading:` carries no reason"},
			mutate: func(f map[string]string) {
				replaceIn(f, providerCatalog, "`proposed:canary(#7)`", "`proposed:canary`")
				replaceIn(f, providerCatalog, "`reading:` whether it is meant needs a person.", "`reading:` no.")
			}},
		{name: "a span of a table row that only looks like a token is prose", opts: providerOptions(), mutate: func(f map[string]string) {
			replaceIn(f, providerCatalog, "`make:testacc`; `proposed", "`make:testacc`, `tset:x`, `test:`, `host:port`; `proposed")
		}},
		{name: "the routing table is not read for names", opts: providerOptions(), mutate: func(f map[string]string) {
			replaceIn(f, providerCatalog, "| A resource | [DC-P01](#dc-p01) |", "| A resource | [DC-P01](#dc-p01), `make:nope` |")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files := providerRepo()
			tc.mutate(files)
			problems, err := Check(writeRepo(t, files), tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			wantProblems(t, problems, tc.want)
		})
	}
}

// wantProblems fails unless each text is contained in a different problem and every problem is wanted.
func wantProblems(t *testing.T, problems []Problem, want []string) {
	t.Helper()
	used := make([]bool, len(problems))
	for _, w := range want {
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
}

func TestOptionsAreValidated(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantErr string // empty: valid
	}{
		{name: "the defaults", opts: DefaultOptions()},
		{name: "one letter", opts: Options{Catalog: providerCatalog, Letters: "P"}},
		{name: "a path that is cleaned", opts: Options{Catalog: "./contributing//defect-classes.md", Letters: "P"}},
		{name: "no catalog", opts: Options{Letters: "P"}, wantErr: "must be a path below the repository root"},
		{name: "an absolute catalog", opts: Options{Catalog: "/etc/defect-classes.md", Letters: "P"}, wantErr: "must be a path below"},
		{name: "a catalog above the root", opts: Options{Catalog: "../defect-classes.md", Letters: "P"}, wantErr: "must be a path below"},
		{name: "no letters", opts: Options{Catalog: providerCatalog}, wantErr: "must be capital letters"},
		{name: "a lower case letter", opts: Options{Catalog: providerCatalog, Letters: "p"}, wantErr: "must be capital letters"},
		{name: "two letters in one item", opts: Options{Catalog: providerCatalog, Letters: "CL"}, wantErr: "must be capital letters"},
		{name: "a trailing comma", opts: Options{Catalog: providerCatalog, Letters: "C,"}, wantErr: "must be capital letters"},
		{name: "a letter twice", opts: Options{Catalog: providerCatalog, Letters: "C,L,C"}, wantErr: "letter C is listed twice"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newRules(tc.opts)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestTheCommandTakesTheCatalogAndTheIdLetters(t *testing.T) {
	repo := writeRepo(t, providerRepo())
	tests := []struct {
		name       string
		args       []string
		wantStatus int
		wantErr    string
	}{
		{name: "both flags", args: []string{"-root", repo, "-catalog", providerCatalog, "-ids", "P"}, wantStatus: 0},
		{name: "the catalog flag alone leaves the library's letters, which the page does not define", args: []string{"-root", repo, "-catalog", providerCatalog}, wantStatus: 1, wantErr: "problem(s)"},
		{name: "the defaults look for the library's catalog", args: []string{"-root", repo}, wantStatus: 2, wantErr: "reading the catalog"},
		{name: "a bad letter list", args: []string{"-root", repo, "-catalog", providerCatalog, "-ids", "p"}, wantStatus: 2, wantErr: "must be capital letters"},
		{name: "a catalog above the root", args: []string{"-root", repo, "-catalog", "../x.md"}, wantStatus: 2, wantErr: "must be a path below"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(tc.args, &out, &errOut); got != tc.wantStatus {
				t.Fatalf("status = %d, want %d (stdout %q, stderr %q)", got, tc.wantStatus, out.String(), errOut.String())
			}
			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}
		})
	}
}
