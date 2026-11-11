package main

import (
	"maps"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// The cases are the files of testdata/src/lockwait: the shape of escp-go#154 in historic.go and the
// other blocking operations under a lock must be reported (a line marked `// want` is, no other line
// may be), and the fixed shape, the released lock and the annotated waits in fixed.go must stay silent.
// The lock wrappers of wrappers.go are the default of the flag, the escp-go client's.
func TestLockwait(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newLockwait(), "lockwait")
}

// Without the wrapper names the wrappers are plain functions, and nothing in wrappers.go is held.
func TestLockwaitWithoutWrappers(t *testing.T) {
	a := newLockwait()
	if err := a.Flags.Set("lockfuncs", ""); err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), a, "nowrappers")
}

func TestParseLockFuncs(t *testing.T) {
	tests := []struct {
		name string
		list string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"names", "lock,acquire", map[string]string{"lock": "", "acquire": ""}},
		{"fields", "lock=mu, beginModify = modify ,acquire", map[string]string{"lock": "mu", "beginModify": "modify", "acquire": ""}},
		{"stray commas", ",lock,,", map[string]string{"lock": ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseLockFuncs(tc.list); !maps.Equal(got, tc.want) {
				t.Fatalf("parseLockFuncs(%q) = %v, want %v", tc.list, got, tc.want)
			}
		})
	}
}

// Test files are not checked unless asked: a test may hold a lock around a wait on purpose.
func TestLockwaitTestFiles(t *testing.T) {
	skipped := newLockwait()
	analysistest.Run(t, analysistest.TestData(), skipped, "skiptests")

	checked := newLockwait()
	if err := checked.Flags.Set("tests", "true"); err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), checked, "lockwaittests")
}
