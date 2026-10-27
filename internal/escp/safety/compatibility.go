package safety

import (
	_ "embed" // the compatibility table is embedded
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// Support says how far a hardware and firmware combination has been verified.
type Support string

// The levels of support, from verified to refused.
const (
	// SupportVerified: reads and writes were checked on a real switch. Only the builds of
	// verified rows may write without the user's opt-in.
	SupportVerified Support = "verified"
	// SupportUntried: expected to work, never tried. Reads work; writes need the opt-in.
	SupportUntried Support = "untried"
	// SupportUntested: nothing is known.
	SupportUntested Support = "untested"
	// SupportUnsupported: known not to work.
	SupportUnsupported Support = "unsupported"
)

// Row is one line of the compatibility table: a switch model, a hardware revision and a firmware,
// and what is known about the combination. The table is compatibility.json, embedded in the
// package; it is the one place the README table and the firmware allow-list come from.
type Row struct {
	Model    string           `json:"model"`
	Hardware string           `json:"hardware"`
	Firmware string           `json:"firmware"`
	Builds   []escp.BuildDate `json:"builds,omitempty"` // the build dates the row covers
	Support  Support          `json:"support"`
	Summary  string           `json:"summary,omitempty"` // markdown, what was verified or what to do
}

//go:embed compatibility.json
var compatibilityJSON []byte

// compatibility is the parsed table; it is loaded once and validated, and a table that is not
// valid stops the program at start, because the firmware gate depends on it.
var compatibility = mustLoadCompatibility(compatibilityJSON)

func mustLoadCompatibility(data []byte) []Row {
	rows, err := parseCompatibility(data)
	if err != nil {
		panic("safety: compatibility.json: " + err.Error())
	}
	return rows
}

// parseCompatibility reads and validates the table: known support levels, well-formed and unique
// build dates, and builds only on rows that name them (a verified row must).
func parseCompatibility(data []byte) ([]Row, error) {
	var doc struct {
		Rows []Row `json:"rows"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	seen := map[escp.BuildDate]bool{}
	for i, r := range doc.Rows {
		switch r.Support {
		case SupportVerified, SupportUntried, SupportUntested, SupportUnsupported:
		default:
			return nil, fmt.Errorf("row %d: unknown support %q", i+1, r.Support)
		}
		if r.Model == "" || r.Hardware == "" || r.Firmware == "" {
			return nil, fmt.Errorf("row %d: model, hardware and firmware are required", i+1)
		}
		if r.Support == SupportVerified && len(r.Builds) == 0 {
			return nil, fmt.Errorf("row %d: a verified row names the builds it covers", i+1)
		}
		for _, b := range r.Builds {
			if seen[b] {
				return nil, fmt.Errorf("row %d: build %s appears twice", i+1, b)
			}
			seen[b] = true
		}
	}
	return doc.Rows, nil
}

// Compatibility returns the compatibility table. The result is a copy.
func Compatibility() []Row {
	out := make([]Row, len(compatibility))
	for i, r := range compatibility {
		r.Builds = slices.Clone(r.Builds)
		out[i] = r
	}
	return out
}

// verifiedBuilds lists the builds of the verified rows, in order.
func verifiedBuilds(rows []Row) []escp.BuildDate {
	var out []escp.BuildDate
	for _, r := range rows {
		if r.Support == SupportVerified {
			out = append(out, r.Builds...)
		}
	}
	slices.Sort(out)
	return out
}

// CompatibilityTable renders the table as the markdown the README shows.
func CompatibilityTable() string {
	return renderCompatibility(compatibility)
}

func renderCompatibility(rows []Row) string {
	var b strings.Builder
	b.WriteString("| Switch | Hardware | Firmware | Status |\n|---|---|---|---|\n")
	for _, r := range rows {
		status := map[Support]string{
			SupportVerified: "**Verified**", SupportUntried: "Untried", SupportUntested: "Untested", SupportUnsupported: "Not supported",
		}[r.Support]
		if r.Summary != "" {
			sep := "; "
			if r.Support == SupportVerified || r.Support == SupportUnsupported {
				sep = ": "
			}
			status += sep + r.Summary
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.Model, r.Hardware, r.Firmware, status)
	}
	return b.String()
}
