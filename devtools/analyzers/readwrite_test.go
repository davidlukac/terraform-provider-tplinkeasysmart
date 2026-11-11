package main

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// The cases are the files of testdata/src/readwrite: the shapes of escp-go#142 and #184 in historic.go
// must be reported (a line marked `// want` is, no other line may be), and the fixed shapes in fixed.go
// and the annotated function must stay silent.
func TestReadwrite(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newReadwrite(), "readwrite")
}

// Without a receiver filter a type that is not the client is mistaken for it, by name.
func TestReadwriteNameCollision(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newReadwrite(), "collision")
}

// With the filter the same names on another type are not steps.
func TestReadwriteReceiverFilter(t *testing.T) {
	a := newReadwrite()
	if err := a.Flags.Set("receivers", `receivers\.Client$`); err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), a, "receivers")
}

// A pattern that does not compile is an error of the run, naming its flag.
func TestCompileReadwrite(t *testing.T) {
	good := readwriteConfig{readers: "a", writers: "b", guards: "c"}
	tests := []struct {
		name string
		edit func(*readwriteConfig)
		want string
	}{
		{"readers", func(c *readwriteConfig) { c.readers = "(" }, "-readwrite.readers"},
		{"writers", func(c *readwriteConfig) { c.writers = "(" }, "-readwrite.writers"},
		{"guards", func(c *readwriteConfig) { c.guards = "(" }, "-readwrite.guards"},
		{"receivers", func(c *readwriteConfig) { c.receivers = "(" }, "-readwrite.receivers"},
		{"valid", func(c *readwriteConfig) { c.receivers = "x" }, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.edit(&cfg)
			_, err := compileReadwrite(&cfg)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want an error naming %s, got %v", tc.want, err)
			}
		})
	}
}

// Test files are not checked unless asked.
func TestReadwriteTestFiles(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newReadwrite(), "skiptests")

	checked := newReadwrite()
	if err := checked.Flags.Set("tests", "true"); err != nil {
		t.Fatal(err)
	}
	analysistest.Run(t, analysistest.TestData(), checked, "readwritetests")
}
