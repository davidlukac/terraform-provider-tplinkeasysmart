// Command lintcheck proves that the layering and forbidigo rules of .golangci.yml still fire. It runs
// the repository's own configuration over the fixture module (devtools/lintfixtures), which holds one
// violation of every depguard rule and every forbidigo pattern and, in the places a rule leaves out, code
// that must stay clean, and compares what the linter reports with the expected list
// (devtools/lintfixtures/expected.txt): every expected finding must be there and nothing else.
//
//	go -C devtools run ./lintcheck -root . -lint "$(go tool -modfile=devtools/go.mod -n golangci-lint)"
//
// The same check holds another configuration to its own fixture module, with the linters the configuration
// enables (`-linters ""`): `make lintfixtures` runs the dead-code configuration (.golangci-deadcode.yml,
// `unused` alone with the tests left out) over devtools/deadcodefixtures, where a function that only a test
// calls must be reported, so that a configuration weakened to `tests: true` or to another linter fails.
//
//	go -C devtools run ./lintcheck -root . -lint "$(...)" -config .golangci-deadcode.yml -fixtures devtools/deadcodefixtures -linters ""
//
// A rule whose pattern stops matching, an exclusion whose path or text now swallows a fixture, a rule
// relaxed or a linter upgrade that resolves a call differently each leave a finding missing or one more
// than expected, and the command exits 1 with one line for each. With -print it prints what the linter
// reported, as lines of the expected list, which is how a fixture of a new rule gets its entries.
//
// The linter runs on a copy of the fixtures next to a copy of .golangci.yml, in a temporary directory,
// because the paths of the exclusions of that file (`^(transport|discover)\.go$`) are relative to the
// directory of the configuration: run in place, the fixtures would sit at devtools/lintfixtures/ and
// the path rules would be tested against a different name than the real files have.
//
// It exits 0 when the findings are as expected, 1 when they differ and 2 for a usage error or a linter
// that did not run.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Exit statuses.
const (
	exitOK      = 0
	exitDiffers = 1
	exitError   = 2
)

// defaultFixtures is the fixture module, relative to the root of the repository.
const defaultFixtures = "devtools/lintfixtures"

// defaultLinters are the linters the fixtures exercise. The run is limited to them so that a linter added
// to the configuration later does not have to be satisfied by the fixtures: the rules are what is under test.
const defaultLinters = "depguard,forbidigo"

// configName is the configuration the linter reads from the root of the repository, unless -config names
// another. The linter always finds it in the working directory under this name, whatever it is called in
// the repository.
const configName = ".golangci.yml"

// expectedName is the list of the expected findings, in the fixture module.
const expectedName = "expected.txt"

// main is the process entry point.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, execLinter))
}

// options are the parsed flags.
type options struct {
	root, lint, fixtures, linters, config string
	print                                 bool
}

// parseOptions reads the flags; it returns false after reporting a usage error.
func parseOptions(args []string, stderr io.Writer) (options, bool) {
	var o options
	fs := flag.NewFlagSet("lintcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&o.root, "root", ".", "the root of the repository (where .golangci.yml is)")
	fs.StringVar(&o.lint, "lint", "", "the golangci-lint binary (required)")
	fs.StringVar(&o.fixtures, "fixtures", defaultFixtures, "the fixture module, relative to the root")
	fs.StringVar(&o.linters, "linters", defaultLinters, "the linters to run, comma-separated; empty: the ones the configuration enables")
	fs.StringVar(&o.config, "config", configName, "the configuration file, relative to the root")
	fs.BoolVar(&o.print, "print", false, "print the findings as lines of the expected list and exit")
	if err := fs.Parse(args); err != nil {
		return o, false
	}
	if o.lint == "" {
		fmt.Fprintln(stderr, "lintcheck: -lint is required")
		return o, false
	}
	return o, true
}

// run checks the fixtures and returns the exit status. lint runs the linter; the tests replace it.
func run(args []string, stdout, stderr io.Writer, lint linterRunner) int {
	o, ok := parseOptions(args, stderr)
	if !ok {
		return exitError
	}
	actual, err := lintFixtures(o, lint)
	if err != nil {
		fmt.Fprintf(stderr, "lintcheck: %v\n", err)
		return exitError
	}
	if o.print {
		for _, line := range sorted(actual) {
			fmt.Fprintln(stdout, line)
		}
		return exitOK
	}
	text, err := os.ReadFile(filepath.Join(o.root, o.fixtures, expectedName))
	if err != nil {
		fmt.Fprintf(stderr, "lintcheck: %v\n", err)
		return exitError
	}
	expected, err := parseExpected(string(text))
	if err != nil {
		fmt.Fprintf(stderr, "lintcheck: %v\n", err)
		return exitError
	}
	missing, unexpected := compare(expected, actual)
	for _, line := range missing {
		fmt.Fprintf(stdout, "missing (a rule no longer fires, or an exclusion swallows its fixture): %s\n", line)
	}
	for _, line := range unexpected {
		fmt.Fprintf(stdout, "unexpected (a fixture that must stay clean is reported, or a rule is wider than it was): %s\n", line)
	}
	if len(missing)+len(unexpected) > 0 {
		return exitDiffers
	}
	fmt.Fprintf(stdout, "lintcheck: %d findings, all expected, nothing else\n", len(actual))
	return exitOK
}
