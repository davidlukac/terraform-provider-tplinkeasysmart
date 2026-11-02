package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
)

// result is the policy applied to a profile.
type result struct {
	total, covered, excluded int
	uncovered                []uncoveredBlock
	exclusions               []exclusionTotal
	problems                 []string
}

type uncoveredBlock struct {
	span
	statements int
}

// exclusionTotal is what one excluded function takes out of the policy.
type exclusionTotal struct {
	file       string
	line       int
	reason     string
	statements int
}

// evaluate applies the policy: a block is covered when the tests ran it, excluded when it is
// inside a function marked `coverage: ignore - <reason>`, and uncovered otherwise.
func evaluate(blocks map[span]counts, root string) result {
	marks := map[string][]exclusion{}
	var r result
	for key := range blocks {
		if _, done := marks[key.file]; !done {
			var problems []string
			marks[key.file], problems = markedFunctions(filepath.Join(root, key.file))
			r.problems = append(r.problems, problems...)
		}
	}
	keys := make([]span, 0, len(blocks))
	for key := range blocks {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end < b.end
		}
		if a.startCol != b.startCol {
			return a.startCol < b.startCol
		}
		return a.endCol < b.endCol
	})
	totals := map[[3]string]*exclusionTotal{}
	var order []*exclusionTotal
	for _, key := range keys {
		c := blocks[key]
		r.total += c.statements
		if c.hits > 0 {
			r.covered += c.statements
			continue
		}
		ex, ok := enclosing(marks[key.file], key)
		if !ok {
			r.uncovered = append(r.uncovered, uncoveredBlock{span: key, statements: c.statements})
			continue
		}
		r.excluded += c.statements
		id := [3]string{key.file, strconv.Itoa(ex.start), ex.reason}
		if totals[id] == nil {
			totals[id] = &exclusionTotal{file: key.file, line: ex.start, reason: ex.reason}
			order = append(order, totals[id])
		}
		totals[id].statements += c.statements
	}
	for _, t := range order {
		r.exclusions = append(r.exclusions, *t)
	}
	return r
}

// enclosing finds the excluded function that holds the block.
func enclosing(marks []exclusion, key span) (exclusion, bool) {
	for _, m := range marks {
		if m.start <= key.start && key.end <= m.end {
			return m, true
		}
	}
	return exclusion{}, false
}

// writeReport prints the policy result and returns the exit code: violated when a statement is
// uncovered or a marker is wrong.
func writeReport(w io.Writer, r result) int {
	measured := r.total - r.excluded
	pct := 100.0
	if measured > 0 {
		pct = 100 * float64(r.covered) / float64(measured)
	}
	_, _ = fmt.Fprintf(w, "statements: %d  covered: %d  excluded: %d  uncovered: %d\n", r.total, r.covered, r.excluded, r.total-r.covered-r.excluded)
	_, _ = fmt.Fprintf(w, "effective coverage (excluded statements not counted): %.1f%%\n", pct)
	for _, e := range r.exclusions {
		_, _ = fmt.Fprintf(w, "  excluded %d statement(s) in %s:%d - %s\n", e.statements, e.file, e.line, e.reason)
	}
	for _, u := range r.uncovered {
		_, _ = fmt.Fprintf(w, "  UNCOVERED %s:%d-%d (%d statement(s))\n", u.file, u.start, u.end, u.statements)
	}
	for _, p := range r.problems {
		_, _ = fmt.Fprintln(w, "  PROBLEM "+p)
	}
	if len(r.uncovered) > 0 || len(r.problems) > 0 {
		return exitViolated
	}
	return exitOK
}
