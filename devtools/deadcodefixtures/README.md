# Dead-code fixtures

A module of its own that holds one function only a test calls (`testonly.go`), one that production code
calls and one exported function nothing calls (`used.go`), and, in `internal/regression/`, a function only
a test calls in the directory that the dead-code configuration leaves out. `make deadcodefixtures` (part of
`make lint`, run by `scripts/deadcode-fixtures.sh`) runs the dead-code configuration, `.golangci-deadcode.yml`,
over a copy of the module, next to a copy of the configuration (the paths of its exclusion are relative to
the directory of the file, so the module is checked at the same relative names as the real files), and
compares what the linter reports with [expected.txt](expected.txt): `onlyATest` must be reported and nothing
else.

A configuration weakened so that `make deadcode` can no longer find code only a test uses (`run.tests: true`,
another linter instead of `unused`, no linter at all) leaves the finding missing and fails the target; a path
rule dropped or widened leaves a finding in `internal/regression/` that is not expected, or hides one that is
(DC-C05, issue #218). The ordinary `unused` run of the repository would not report `onlyATest`: it has the
tests on.

The fixtures only have to compile: they are never run.
