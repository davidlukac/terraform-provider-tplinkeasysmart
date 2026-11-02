// Command mutationreport turns the JSON report of a mutation run (gremlins, `make mutation`) into
// the Markdown of a job summary: the count per outcome, then the mutants that survived. It is a build
// tool of this repository, a copy of the one in github.com/davidlukac/escp-go without its badge mode.
//
//	go run ./tools/mutationreport build/mutation.json >> "$GITHUB_STEP_SUMMARY"
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Exit codes of the command.
const (
	exitOK    = 0
	exitUsage = 2
)

// Outcomes of a mutant, as gremlins spells them.
const (
	statusLived = "LIVED"
)

// report is the part of gremlins' JSON report this command reads.
type report struct {
	Files []struct {
		Name      string `json:"file_name"`
		Mutations []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		} `json:"mutations"`
	} `json:"files"`
}

// main is the process entry point.
//
// coverage: ignore - process entry point; run holds all the logic and is tested.
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the whole command: args are the command line without the program name.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: mutationreport REPORT.json")
		return exitUsage
	}
	_, _ = fmt.Fprintln(stdout, "### Mutation testing")
	_, _ = fmt.Fprintln(stdout)
	text, err := os.ReadFile(args[0]) //nolint:gosec // the report the operator names on the command line
	if err != nil {
		_, _ = fmt.Fprintln(stdout, "No report: the run stopped before it wrote one.")
		return exitOK
	}
	var r report
	if err := json.Unmarshal(text, &r); err != nil {
		_, _ = fmt.Fprintf(stdout, "The report cannot be read: %v\n", err)
		return exitOK
	}
	counts, survivors := summarize(r)
	_, _ = fmt.Fprintln(stdout, counts)
	if len(survivors) > 0 {
		_, _ = fmt.Fprintln(stdout)
		_, _ = fmt.Fprintln(stdout, "Surviving mutants (a behavior no test pins; an equivalent mutant is not a reason to bend a test):")
		_, _ = fmt.Fprintln(stdout)
		for _, s := range survivors {
			_, _ = fmt.Fprintln(stdout, s)
		}
	}
	return exitOK
}

// summarize counts the mutants per outcome ("KILLED: 16, LIVED: 1") and lists the ones that lived,
// one Markdown bullet each, in the order of the report.
func summarize(r report) (counts string, survivors []string) {
	byStatus := map[string]int{}
	for _, f := range r.Files {
		for _, m := range f.Mutations {
			byStatus[m.Status]++
			if m.Status == statusLived {
				survivors = append(survivors, fmt.Sprintf("- `%s:%d:%d` %s", f.Name, m.Line, m.Column, m.Type))
			}
		}
	}
	if len(byStatus) == 0 {
		return "No mutants in scope.", nil
	}
	names := make([]string, 0, len(byStatus))
	for s := range byStatus {
		names = append(names, s)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, s := range names {
		parts[i] = fmt.Sprintf("%s: %d", s, byStatus[s])
	}
	return strings.Join(parts, ", "), survivors
}
