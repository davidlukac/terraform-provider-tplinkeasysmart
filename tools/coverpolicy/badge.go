package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// badgeJSON is a shields.io endpoint file.
type badgeJSON struct {
	SchemaVersion int    `json:"schemaVersion"`
	Label         string `json:"label"`
	Message       string `json:"message"`
	Color         string `json:"color"`
}

// colourBands are the lowest percentages of each colour, best first; anything lower is red.
var colourBands = []struct {
	from   float64
	colour string
}{{95, "brightgreen"}, {90, "green"}, {80, "yellowgreen"}, {70, "yellow"}, {60, "orange"}}

func colour(pct float64) string {
	for _, b := range colourBands {
		if pct >= b.from {
			return b.colour
		}
	}
	return "red"
}

// writeBadge prints the measured statement coverage of the whole module: covered statements over
// all statements, with the `coverage: ignore` exclusions counted as NOT covered (unlike the policy
// check, which does not count them).
func writeBadge(w io.Writer, blocks map[span]counts) int {
	total, covered := 0, 0
	for _, c := range blocks {
		total += c.statements
		if c.hits > 0 {
			covered += c.statements
		}
	}
	pct := 0.0
	if total > 0 {
		pct = 100 * float64(covered) / float64(total)
	}
	out, _ := json.Marshal(badgeJSON{SchemaVersion: 1, Label: "coverage", Message: fmt.Sprintf("%.1f%%", pct), Color: colour(pct)}) // a struct of strings and an int always marshals
	_, _ = fmt.Fprintln(w, string(out))                                                                                             // a write to a closed stdout has nowhere to report to
	return exitOK
}
