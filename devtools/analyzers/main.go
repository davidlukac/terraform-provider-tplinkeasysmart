// Command analyzers runs the project's own static checks, the ones golangci-lint has no rule for:
//
//	go -C devtools build -o ../build/analyzers ./analyzers && build/analyzers ./...
//
// It is one multichecker, so every check is selected and configured with a flag prefixed by its name
// (`-lockwait.tests`, `-readwrite.writers=...`); see README.md for what each one reports and how to add
// another. `make lint` runs it next to the `subtests` check.
//
// This directory is the single source of the checks: it is one package with no import of another
// directory of this module, so the workspace's scripts/sync-subtests can copy it verbatim into the
// other repositories (whose devtools module has another path). Change it here, then sync.
package main

import "golang.org/x/tools/go/analysis/multichecker"

// main is the process entry point.
func main() { multichecker.Main(Readwrite, Lockwait) }
