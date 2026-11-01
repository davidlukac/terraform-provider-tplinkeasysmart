package provider

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidlukac/escp-go/safety"
)

var updateReadme = flag.Bool("update-readme", false, "rewrite the compatibility table in README.md from the escp-go compatibility table")

const (
	readmeBegin = "<!-- compatibility-table:begin (generated from the safety package of github.com/davidlukac/escp-go: make compat-table) -->"
	readmeEnd   = "<!-- compatibility-table:end -->"
)

// The README shows the compatibility table of the ESCP library the provider is built with
// (safety.CompatibilityTable), which is also the table behind the firmware gate, so the
// documentation cannot differ from what the gate enforces. `make compat-table` rewrites it; a
// library release that verifies another build changes it, and Dependabot's bump of the module
// fails this test until the README is regenerated.
func TestReadmeCompatibilityTable(t *testing.T) {
	path := filepath.Join("..", "..", "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	begin, end := strings.Index(text, readmeBegin), strings.Index(text, readmeEnd)
	if begin < 0 || end < begin {
		t.Fatalf("README.md needs the markers\n%s\n...\n%s", readmeBegin, readmeEnd)
	}
	want := text[:begin+len(readmeBegin)] + "\n\n" + safety.CompatibilityTable() + "\n" + text[end:]
	if *updateReadme {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil { //nolint:gosec // a documentation file
			t.Fatal(err)
		}
		return
	}
	if text != want {
		t.Fatal("the compatibility table in README.md is out of date: run `make compat-table`")
	}
}
