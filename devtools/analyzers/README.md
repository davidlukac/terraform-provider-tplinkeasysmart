# analyzers

Project checks that `golangci-lint` has no rule for, written as `go/analysis` passes and run by
`make lint` as one `multichecker` binary (`build/analyzers ./...`), next to `devtools/subtests`.
Each exists because the defect it finds was found late, more than once.

| Check | Reports | Historic defect |
|---|---|---|
| `readwrite` | a function that reads a setting and then writes one without holding the modify guard across both | escp-go#142 (six methods), #184 (two limit guards) |
| `lockwait` | a blocking operation (channel, `time.Sleep`, `WaitGroup.Wait`, a socket read or write, a call that takes a context) between taking a lock and releasing it | escp-go#154 (the firmware gate held its lock across a read of the switch) |
| `validatecall` | an exported function or method of the root package that takes a value of a type with a `Validate` method and never calls it (or a helper that does), or uses the value before it does | escp-go#169 (an empty `SetPortPriorities` sent a full rewrite), and the class of seven releases of input validation fixed one entry at a time |
| `nosleep` | a `time.Sleep` in a test or in production code, and in a test a `time.After` that is not the failure bound of a `select` case (a case that calls `t.Fatal`, `t.Error` and the like as one of its own statements, or panics) | the flaky tests of 0.6.0 to 0.9.0 (escp-go#195, DC-C06): a guess that is too short flakes, and one that is wrong passes without reaching the path |

## The annotations

An intended case carries its reason, so the next reader knows what ends the wait or why the read is safe.
An annotation without a reason is itself reported.

- `// lockwait: <reason>` on the blocking operation, on the line above it, on the line of the `Lock` (or of
  `c.lock(ctx)`, `c.beginModify(ctx)`: the whole critical section) or in the function's doc comment (the
  whole function). Every lock held at the operation must be explained, so a reason on an inner section does
  not cover the outer one. Write what makes the wait end: the caller's context, `Close`, a deadline or budget.
- `// readwrite: <reason>` in the doc comment of the function.
- `// validatecall: <reason>` in the doc comment of the function (every parameter), or on the line of a parameter or the line above it (that parameter). Write where the rule is applied instead (the callee that validates, the layer below) or why the value needs none.
- `// nosleep: <reason>` on the call's line, on the line above it or in the function's doc comment. Write what
  the test needs the real clock for: a deadline behavior through a real socket, a live test against a real
  switch, a poll of a state that has no signal. Most waits have an event; the test waits for it (a channel, a
  context, a signal from a seam, a line the library logs), and a state with no event is polled with a bound by a
  helper that does not pause.

## Running one

```
go -C devtools build -o ../build/analyzers ./analyzers
build/analyzers ./...                       # every check
build/analyzers -readwrite=false ./...      # one check off (a flag named after it)
build/analyzers -lockwait.tests ./...       # a check's own flag; -help lists them all
GOFLAGS=-tags=live build/analyzers ./...    # the files of another build tag (the live tests)
go -C devtools test ./analyzers             # the fixtures
```

Configuration is by flag, with the escp-go client's names as the defaults (`-readwrite.readers`,
`-readwrite.writers`, `-readwrite.guards`, `-readwrite.receivers`, `-lockwait.lockfuncs`, `-validatecall.packages`, `-validatecall.names`); the doc comment of
each analyzer (`Readwrite`, `Lockwait`, `Validatecall`) is the reference. Another repository passes its own in its Makefile.

## Limits

Both work inside one function, in source order: a callee that takes the lock for its caller is seen at the
caller's call, a closure handed to a call made under a lock is reported at the call and walked as a function of
its own. `lockwait` does not model a lock taken in another goroutine, and does not see that `Close` is waiting
behind a stuck call (the defect of escp-go#162) because nothing in the function says so: the tests that cancel
a call in flight pin that. `nosleep` sees a wait on the clock, not a wait that is too long on a channel: a `select` case that fails the test
after a timeout is accepted as the bound of a wait that has a signal, so a long timeout used as if it were the event,
a channel nothing sends to, and a timer made with `time.NewTimer`, `time.Tick` or `time.AfterFunc` are read in review.
`readwrite` matches methods by name (and optionally by receiver type), so a type
that is not the client with a method of the same name is a false hit unless `-readwrite.receivers` is set.

`validatecall` is syntactic and works inside the package it is pointed at (by default the root package of
escp-go: the exported API that takes a caller's value). It follows the parameter through assignments and
ranges and through the helpers of the package that call a `Validate` method, but not into another package,
through an interface or a field; a helper that validates something other than the parameter it is handed is
taken for one that validates it; a struct with no `Validate` of its own is not asked to validate the fields
that have one; and it says nothing about whether the rule itself is right (the defect of most of the seven
releases: a rule that accepts `"+3"`), which the tables of valid and invalid values do. Pointed at the other
packages of the module it reports lookups that answer "no" to an invalid value (`PortSet.Has`, `UseOf`),
which is why they are not in its default.

## Adding one

1. A file `devtools/analyzers/<name>.go` with an exported `var <Name> = new<Name>()` returning an
   `*analysis.Analyzer` whose configuration is flags bound to a struct held by its closure (no package-level
   state, so a test can build a fresh one with other flags), and a doc comment that says what it reports, the
   annotation and every flag. Share `annotation.go` for a `// <marker>: <reason>` comment.
2. Add it to the `multichecker.Main(...)` call in `main.go`.
3. A fixture under `testdata/src/<name>/` that reproduces the historic defect as it was written (the line the
   check must report carries `// want "<regexp>"`), a file with the fixed shape that must stay silent, and one
   with the annotated case; a test `<name>_test.go` that runs `analysistest.Run`. Prove the check fails on the
   historic shape before you rely on it.
4. Run it on the library (`build/analyzers ./...`): it must be silent on `main`, with a reason written at
   every intended case.

The directory is one package with no import of another directory of the module: it is copied verbatim into
the other repositories (their `devtools` module has another path) by a script that lives outside this repository, so it stays
one directory, and a new analyzer is a new file in it.
