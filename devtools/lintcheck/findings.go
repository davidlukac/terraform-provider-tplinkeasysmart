package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// finding is one report of the linter: the file (relative to the fixture module, slash-separated), the
// linter that made it and its message. The line is not part of it: a fixture can move a line without
// the list of expected findings changing, and two findings of one file are told apart by their message.
type finding struct {
	file   string
	linter string
	text   string
}

// String is the form of one line of the expected list: `file: linter: text`.
func (f finding) String() string { return f.file + ": " + f.linter + ": " + f.text }

// separator divides the three fields of a line of the expected list.
const separator = ": "

// fieldCount is the number of fields of a line of the expected list.
const fieldCount = 3

// parseExpected reads the expected list: one finding per line, blank lines and lines that start with `#`
// ignored. A line that does not have the three fields is an error that names it.
func parseExpected(text string) ([]finding, error) {
	var out []finding
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.SplitN(line, separator, fieldCount)
		if len(fields) != fieldCount || slices.Contains(fields, "") {
			return nil, fmt.Errorf("expected list, line %d: want `file: linter: text`, got %q", n+1, line)
		}
		out = append(out, finding{file: fields[0], linter: fields[1], text: fields[2]})
	}
	return out, nil
}

// report is the part of the linter's JSON output this tool reads.
type report struct {
	Issues []struct {
		FromLinter string
		Text       string
		Pos        struct{ Filename string }
	}
}

// readFindings reads the JSON report golangci-lint wrote to path.
func readFindings(path string) ([]finding, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the linter's report: %w", err)
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parsing the linter's report %s: %w", path, err)
	}
	out := make([]finding, 0, len(r.Issues))
	for _, i := range r.Issues {
		out = append(out, finding{file: filepath.ToSlash(i.Pos.Filename), linter: i.FromLinter, text: i.Text})
	}
	return out, nil
}

// compare returns the expected findings the linter did not report and the reported ones that were not
// expected, each sorted. The lists are compared as multisets: a finding expected twice must appear twice.
func compare(expected, actual []finding) (missing, unexpected []string) {
	left := map[finding]int{}
	for _, f := range expected {
		left[f]++
	}
	for _, f := range actual {
		if left[f] > 0 {
			left[f]--
			continue
		}
		unexpected = append(unexpected, f.String())
	}
	for f, n := range left {
		for range n {
			missing = append(missing, f.String())
		}
	}
	slices.Sort(missing)
	slices.Sort(unexpected)
	return missing, unexpected
}

// sorted returns the findings as lines of the expected list, in order.
func sorted(findings []finding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.String())
	}
	slices.Sort(out)
	return out
}
