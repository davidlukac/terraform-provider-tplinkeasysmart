// Command zizmorprobe is the check behind `make zizmorcheck`: it proves that scripts/zizmor.sh (what `make
// zizmor` runs) reports a deliberate finding next to each input of the audited tree that its cases name. The
// cases, the fixtures and the reasons are in the documentation of package probe; it is a build tool of this
// repository, not part of the API.
//
//	ZIZMOR_SELF_REPOSITORY_FINDINGS=13 go run ./tools/zizmorprobe
//
// The program is also the stand-in for zizmor in the probes that record the command line and the
// environment of the call: it behaves so when it is run under the name zizmor.
package main

import (
	"context"
	"os"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/tools/zizmorprobe/probe" //nolint:depguard // a build tool imports its own and its sibling tools' packages; the rule keeps product code from importing a tool
)

// main is the process entry point.
//
// coverage: ignore - process entry point; probe.Run holds all the logic and is tested.
func main() {
	os.Exit(probe.Run(context.Background(), os.Args, probe.ProcessEnvironment()))
}
