# Dead-code fixtures

A module of its own that holds, for the dead-code configuration `.golangci-deadcode.yml`:

- `testonly.go`: a function only a test calls, which must be reported (the linter set and `tests: false`);
- `used.go`: a function production code calls and one exported function nothing calls, which are not reported;
- `internal/regression/harness.go`: a function only a test calls, in the directory the path rule leaves out,
  which is not reported;
- `internal/provider/provider.go`: a function only a test calls, in a directory the path rule must not reach,
  which must be reported.

`make deadcodefixtures` (part of `make lint`, run by `devtools/lintcheck`) runs the configuration over a copy
of the module, next to a copy of the configuration (the paths of its exclusion are relative to the directory
of the file, so the module is checked at the same relative names as the real files), and compares what the
linter reports with [expected.txt](expected.txt): `onlyATest` and `onlyAProviderTest` must be reported and
nothing else.

What it holds: the linter set (`unused` alone), `tests: false`, and that the path rule reaches the harness
directory and the provider directory and no other. A configuration weakened in one of these ways (`run.tests:
true`, another linter instead of `unused`, no linter at all, a path rule that is dropped, broader or that adds
the provider directory) leaves a finding missing or unexpected and fails the target (DC-C05, issue #218). It
does not hold the rest of the file or the `Makefile` recipes. The ordinary `unused` run of the repository would
not report `onlyATest`: it has the tests on.

The fixtures only have to compile: they are never run.
