// Command coverpolicy enforces the module's coverage policy on a Go cover profile, and turns the
// same profile into the shields.io endpoint file behind the README badge.
//
//	coverpolicy check cover.out [module-root]   exit 1 when the policy is violated
//	coverpolicy badge cover.out [module-root]   print the badge JSON
//
// Policy: every statement of the module is covered, except inside functions whose doc comment
// contains `coverage: ignore - <reason>`; the reason is mandatory and every exclusion is listed.
// Test files are not measured, and neither is test-support code (notMeasured): the fake switch in
// escptest is exercised by the library's own tests, so a branch of it that no test reaches is a gap
// in that fake, not in the product. The badge counts the excluded statements as not covered.
//
// It replaces scripts/covercheck.py and scripts/coverbadge.py and uses only the standard library.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = "usage: coverpolicy check|badge cover.out [module-root]\n"

// Exit codes: the policy holds, the policy is violated, the tool could not do its work.
const (
	exitOK       = 0
	exitViolated = 1
	exitUsage    = 2
)

// main is the process entry point.
// coverage: ignore - process entry point; run holds all the logic and is tested.
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run is the whole command: args are the command line without the program name.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || (args[0] != "check" && args[0] != "badge") {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	root := "."
	if len(args) > 2 {
		root = args[2]
	}
	module, err := moduleOf(root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "coverpolicy:", err)
		return exitUsage
	}
	text, err := os.ReadFile(args[1]) //nolint:gosec // the profile the operator names on the command line
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "coverpolicy:", err)
		return exitUsage
	}
	blocks, err := parseProfile(string(text), module)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "coverpolicy:", err)
		return exitUsage
	}
	if args[0] == "badge" {
		return writeBadge(stdout, blocks)
	}
	return writeReport(stdout, evaluate(blocks, root))
}
