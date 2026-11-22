package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseExpected(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    []finding
		wantErr string
	}{
		{name: "empty", text: "", want: nil},
		{name: "comments and blank lines", text: "# a comment\n\n   \n# another\n", want: nil},
		{
			name: "a finding keeps the colons of its text",
			text: "model/a.go: depguard: import 'x' is not allowed from list 'model-layer': because\n",
			want: []finding{{file: "model/a.go", linter: "depguard", text: "import 'x' is not allowed from list 'model-layer': because"}},
		},
		{
			name: "two findings in order",
			text: "a.go: forbidigo: one\nb.go: forbidigo: two",
			want: []finding{{"a.go", "forbidigo", "one"}, {"b.go", "forbidigo", "two"}},
		},
		{name: "two fields only", text: "a.go: forbidigo", wantErr: "line 1"},
		{name: "an empty field", text: "ok.go: forbidigo: fine\na.go: : text", wantErr: "line 2"},
		{name: "no separator", text: "\n\nnothing here", wantErr: "line 3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseExpected(tc.text)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one that names %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseExpected: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindingStringIsTheLineOfTheExpectedList(t *testing.T) {
	f := finding{file: "a/b.go", linter: "forbidigo", text: "use of `x`: no"}
	got, err := parseExpected(f.String())
	if err != nil || len(got) != 1 || got[0] != f {
		t.Fatalf("parseExpected(%q) = %v, %v; want the finding back", f.String(), got, err)
	}
}

func TestCompare(t *testing.T) {
	a := finding{"a.go", "forbidigo", "one"}
	b := finding{"b.go", "depguard", "two"}
	tests := []struct {
		name           string
		expected       []finding
		actual         []finding
		wantMissing    []string
		wantUnexpected []string
	}{
		{name: "equal", expected: []finding{a, b}, actual: []finding{b, a}},
		{name: "both empty"},
		{name: "a rule no longer fires", expected: []finding{a, b}, actual: []finding{a}, wantMissing: []string{b.String()}},
		{name: "a clean fixture is reported", expected: []finding{a}, actual: []finding{a, b}, wantUnexpected: []string{b.String()}},
		{name: "nothing reported", expected: []finding{a}, wantMissing: []string{a.String()}},
		{name: "nothing expected", actual: []finding{a}, wantUnexpected: []string{a.String()}},
		{name: "a twice expected finding must appear twice", expected: []finding{a, a}, actual: []finding{a}, wantMissing: []string{a.String()}},
		{name: "a finding reported twice but expected once", expected: []finding{a}, actual: []finding{a, a}, wantUnexpected: []string{a.String()}},
		{
			name:           "same file and linter, another message",
			expected:       []finding{{"a.go", "forbidigo", "one"}},
			actual:         []finding{{"a.go", "forbidigo", "other"}},
			wantMissing:    []string{"a.go: forbidigo: one"},
			wantUnexpected: []string{"a.go: forbidigo: other"},
		},
		{
			name:           "same message, another file",
			expected:       []finding{{"transport.go", "forbidigo", "one"}},
			actual:         []finding{{"internal/wire/transport.go", "forbidigo", "one"}},
			wantMissing:    []string{"transport.go: forbidigo: one"},
			wantUnexpected: []string{"internal/wire/transport.go: forbidigo: one"},
		},
		{
			name:        "results are sorted",
			expected:    []finding{{"c.go", "x", "t"}, {"a.go", "x", "t"}, {"b.go", "x", "t"}},
			wantMissing: []string{"a.go: x: t", "b.go: x: t", "c.go: x: t"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing, unexpected := compare(tc.expected, tc.actual)
			if !reflect.DeepEqual(missing, tc.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tc.wantMissing)
			}
			if !reflect.DeepEqual(unexpected, tc.wantUnexpected) {
				t.Errorf("unexpected = %v, want %v", unexpected, tc.wantUnexpected)
			}
		})
	}
}

func TestSorted(t *testing.T) {
	got := sorted([]finding{{"b.go", "x", "t"}, {"a.go", "x", "t"}})
	want := []string{"a.go: x: t", "b.go: x: t"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sorted = %v, want %v", got, want)
	}
}

func TestReadFindings(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	t.Run("issues", func(t *testing.T) {
		path := write("ok.json", `{"Issues":[{"FromLinter":"forbidigo","Text":"use of x","Pos":{"Filename":"a/b.go","Line":3}}],"Report":{}}`)
		got, err := readFindings(path)
		if err != nil {
			t.Fatal(err)
		}
		want := []finding{{"a/b.go", "forbidigo", "use of x"}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
	t.Run("no issues", func(t *testing.T) {
		got, err := readFindings(write("none.json", `{"Issues":[]}`))
		if err != nil || len(got) != 0 {
			t.Errorf("got %v, %v; want none", got, err)
		}
	})
	t.Run("null issues", func(t *testing.T) {
		got, err := readFindings(write("null.json", `{"Issues":null}`))
		if err != nil || len(got) != 0 {
			t.Errorf("got %v, %v; want none", got, err)
		}
	})
	t.Run("not json", func(t *testing.T) {
		if _, err := readFindings(write("bad.json", `not json`)); err == nil || !strings.Contains(err.Error(), "bad.json") {
			t.Errorf("err = %v, want one that names the file", err)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, err := readFindings(filepath.Join(dir, "absent.json")); err == nil {
			t.Error("a missing report must be an error: the linter did not run")
		}
	})
}
