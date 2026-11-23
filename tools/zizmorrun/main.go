// Command zizmorrun is what `make zizmor` runs (through scripts/zizmor.sh): it audits a repository with the
// pinned zizmor, with fixed options and a cleaned environment so that no input the audited tree can carry (a
// configuration file, an ignore file, a comment) changes what is audited, and judges the answer against an exact expectation. The options, the variables that are
// cleared and the reasons are in the documentation of package audit; it is a build tool of this repository,
// not part of the API.
//
//	go run ./tools/zizmorrun ROOT EXPECTED
//
// ROOT is the repository to audit and EXPECTED the number of `self-repository` findings it may carry.
package main

import (
	"context"
	"os"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/tools/zizmorrun/audit" //nolint:depguard // a build tool imports its own and its sibling tools' packages; the rule keeps product code from importing a tool
)

// main is the process entry point.
//
// coverage: ignore - process entry point; audit.Run holds all the logic and is tested.
func main() {
	os.Exit(audit.Run(context.Background(), os.Args[1:], audit.ProcessEnvironment()))
}
