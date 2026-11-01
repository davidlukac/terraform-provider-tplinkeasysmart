package safety

import (
	"slices"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

func TestCompatibilityFeedsTheAllowList(t *testing.T) {
	if got, want := VerifiedFirmwareBuilds(), []escp.BuildDate{"20230218", "20250710"}; !slices.Equal(got, want) {
		t.Fatalf("allow-list = %v, want %v", got, want)
	}
	// An untried or unsupported row never adds a build to the allow-list.
	rows := []Row{
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []escp.BuildDate{"20250101"}, Support: SupportVerified},
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []escp.BuildDate{"20260101"}, Support: SupportUntried},
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []escp.BuildDate{"20240101"}, Support: SupportUnsupported},
		{Model: "m", Hardware: "h", Firmware: "f", Builds: []escp.BuildDate{"20230101"}, Support: SupportVerified},
	}
	if got, want := verifiedBuilds(rows), []escp.BuildDate{"20230101", "20250101"}; !slices.Equal(got, want) {
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
