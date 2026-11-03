// Command subtests reports table-style loops in test functions that do not run each case as a
// subtest.
//
//	go tool -modfile=devtools/go.mod subtests ./...
//
// A loop is reported when it ranges over a list of cases written out in the test (a composite
// literal, or a variable the test or the package sets from one), reports through the test's
// *testing.T, and never calls Run on it. Such a loop names its failing case only if its author
// remembered to print it, ends at the first Fatal, and cannot be run for one case; t.Run gives each
// case a name, its own failure and a -run filter. A loop that is not a table of cases says so with a
// comment on the line of the loop or the line above it: `// subtests: <reason>`.
//
// Files built only with a tag (the live tests) are not loaded without it, so they are skipped: a
// failing live case has to stop the test before it writes more to a switch. Only Test functions are
// looked at, not helpers, examples or fuzz targets. golangci-lint has no check for this (paralleltest
// and tparallel ask for t.Parallel, which is another rule).
//
// This directory is the single source of the check: it is copied verbatim into the other
// repositories by the workspace's scripts/sync-subtests, so change it here and sync.
package main

import "golang.org/x/tools/go/analysis/singlechecker"

// main is the process entry point.
func main() { singlechecker.Main(Analyzer) }
