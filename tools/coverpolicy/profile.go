package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// notMeasured are the path prefixes (relative to the module root) of test-support packages. Their
// statements are dropped from the profile, so neither the policy nor the badge counts them.
var notMeasured = []string{"internal/escp/escptest/"}

// blockLine is one line of a cover profile: file:startLine.col,endLine.col statements count.
var blockLine = regexp.MustCompile(`^(.+):(\d{1,9})\.(\d{1,9}),(\d{1,9})\.(\d{1,9}) (\d{1,9}) (\d{1,9})$`)

// span is a block of statements in a file, by its position as the profile gives it: the first and
// last line, and the columns, which tell two blocks on the same lines apart.
type span struct {
	file             string
	start, end       int
	startCol, endCol int
}

// counts is what a block holds: its statements, and how often the tests ran it.
type counts struct{ statements, hits int }

// moduleOf reads the module path from root/go.mod.
func moduleOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod")) //nolint:gosec // the go.mod of the root the operator names
	if err != nil {
		return "", fmt.Errorf("reading the module path: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("%s has no module line", filepath.Join(root, "go.mod"))
}

// parseProfile returns the blocks of a cover profile with file names relative to the module root.
// A block that several test binaries report is merged: its statements once, its hits added up.
func parseProfile(text, module string) (map[span]counts, error) {
	blocks := map[span]counts{}
	lines := strings.Split(text, "\n")
	for _, line := range lines[1:] { // the first line is "mode: ..."
		if line == "" {
			continue
		}
		m := blockLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("unparseable cover profile line: %q", line)
		}
		name := strings.TrimPrefix(m[1], module+"/")
		if skipped(name) {
			continue
		}
		key := span{file: name, start: atoi(m[2]), startCol: atoi(m[3]), end: atoi(m[4]), endCol: atoi(m[5])}
		c := blocks[key]
		c.statements = atoi(m[6])
		c.hits += atoi(m[7])
		blocks[key] = c
	}
	return blocks, nil
}

// skipped reports whether the file belongs to a package that is not measured.
func skipped(name string) bool {
	for _, prefix := range notMeasured {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// atoi converts the digits the profile pattern has already checked (at most nine, so no overflow).
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
