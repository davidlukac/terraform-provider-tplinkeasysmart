// Command analyzers runs the project's own static checks, the ones golangci-lint has no rule for:
//
//	go -C devtools build -o ../build/analyzers ./analyzers && build/analyzers ./...
//
// It is one multichecker, so every check is selected and configured with a flag prefixed by its name
// (`-lockwait.tests`, `-readwrite.writers=...`; `nosleep` has none); see README.md for what each one reports and how to add
// another. `make lint` runs it next to the `subtests` check.
//
// This directory is the single source of the checks: it is one package with no import of another
// directory of this module, so it can be copied verbatim into the other repositories (whose devtools
// module has another path). Change it here only; the copies are never edited by hand.
package main

import "golang.org/x/tools/go/analysis/multichecker"

// main is the process entry point.
func main() { multichecker.Main(Readwrite, Lockwait, Validatecall, Nosleep) }
