package safety

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updateReadme = flag.Bool("update-readme", false, "rewrite the compatibility table in README.md from compatibility.json")

const (
	readmeBegin = "<!-- compatibility-table:begin (generated from internal/escp/safety/compatibility.json: make compat-table) -->"
	readmeEnd   = "<!-- compatibility-table:end -->"
)

func TestCompatibilityFeedsTheAllowList(t *testing.T) {
	if got, want := VerifiedFirmwareBuilds(), []string{"20230218", "20250710"}; !slices.Equal(got, want) {
		t.Fatalf("allow-list = %v, want %v", got, want)
	}
	// An untried or unsupported row never adds a build to the allow-list.
	rows := []Row{
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []string{"20250101"}, Support: SupportVerified},
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []string{"20260101"}, Support: SupportUntried},
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []string{"20240101"}, Support: SupportUnsupported},
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []string{"20230101"}, Support: SupportVerified},
	}
	if got, want := verifiedBuilds(rows), []string{"20230101", "20250101"}; !slices.Equal(got, want) {
		t.Fatalf("verifiedBuilds = %v, want %v (sorted, verified rows only)", got, want)
	}
}

func TestCompatibilityIsACopy(t *testing.T) {
	got := Compatibility()
	got[0].Model = "tampered"
	got[0].Builds[0] = "tampered"
	again := Compatibility()
	if again[0].Model == "tampered" || again[0].Builds[0] == "tampered" {
		t.Fatal("changing the result changed the table")
	}
}

func TestParseCompatibilityRefusals(t *testing.T) {
	row := func(extra string) string {
		return `{"rows":[{"model":"m","hardware":"h","firmware":"f",` + extra + `}]}`
	}
	for _, tt := range []struct{ name, doc, want string }{
		{"not json", `{`, "unexpected end"},
		{"unknown support", row(`"support":"maybe"`), "unknown support"},
		{"no model", `{"rows":[{"hardware":"h","firmware":"f","support":"untested"}]}`, "required"},
		{"verified without builds", row(`"support":"verified"`), "names the builds"},
		{"bad build", row(`"support":"verified","builds":["2025-07"]`), "not a build date"},
		{"duplicate build", `{"rows":[{"model":"m","hardware":"h","firmware":"f","support":"verified","builds":["20250710"]},{"model":"m","hardware":"h","firmware":"g","support":"verified","builds":["20250710"]}]}`, "twice"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := parseCompatibility([]byte(tt.doc)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
	if rows, err := parseCompatibility([]byte(row(`"support":"untested"`))); err != nil || len(rows) != 1 {
		t.Fatalf("a valid row: %v, %v", rows, err)
	}
}

func TestMustLoadCompatibilityStopsOnABadTable(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "compatibility.json") {
			t.Fatalf("recovered %v, want a panic naming compatibility.json", r)
		}
	}()
	mustLoadCompatibility([]byte(`{"rows":[{"support":"maybe"}]}`))
}

func TestRenderCompatibility(t *testing.T) {
	got := renderCompatibility([]Row{
		{Model: "A", Hardware: "v1", Firmware: "f1", Support: SupportVerified, Summary: "reads"},
		{Model: "B", Hardware: "v2", Firmware: "f2", Support: SupportUntried, Summary: "reads work"},
		{Model: "C", Hardware: "v3", Firmware: "f3", Support: SupportUntested},
		{Model: "D", Hardware: "v4", Firmware: "f4", Support: SupportUnsupported, Summary: "no key exchange"},
	})
	want := "| Switch | Hardware | Firmware | Status |\n|---|---|---|---|\n" +
		"| A | v1 | f1 | **Verified**: reads |\n" +
		"| B | v2 | f2 | Untried; reads work |\n" +
		"| C | v3 | f3 | Untested |\n" +
		"| D | v4 | f4 | Not supported: no key exchange |\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// The README shows the table generated from compatibility.json, so the documentation cannot
// differ from what the firmware gate enforces. `make compat-table` rewrites it.
func TestReadmeCompatibilityTable(t *testing.T) {
	path := filepath.Join("..", "..", "..", "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	begin, end := strings.Index(text, readmeBegin), strings.Index(text, readmeEnd)
	if begin < 0 || end < begin {
		t.Fatalf("README.md needs the markers\n%s\n...\n%s", readmeBegin, readmeEnd)
	}
	want := text[:begin+len(readmeBegin)] + "\n\n" + CompatibilityTable() + "\n" + text[end:]
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
