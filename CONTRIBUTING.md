# Contributing

Issues and pull requests are welcome. Open an issue first for a new setting. This page is the checklist for **any change to the client, a resource or a data source**. Tick every line that applies, or write in the pull request why it does not. The short version of the pull request checklist is in `.github/pull_request_template.md`; this is the full one.

Tags: **[C]** client change (`internal/escp`), **[R]** new or changed resource, **[D]** new or changed data source. A line without a tag applies to all three.

## 0. Before you start

- [ ] Run `make hooks` once per clone. The pre-commit hook runs format, vet, lint, tests with `-race` and the coverage policy, and must never be bypassed. It does **not** run the Terraform scenarios of `internal/regression`: they skip unless `TF_ACC=1` is set (a skipped scenario still reports `ok`), and CI runs them with `make testacc`. Run `make regression` yourself before you push (about two minutes; needs `terraform` on the `PATH`).
- [ ] Work on a branch (`feat/`, `fix/`, `docs/`, `test/`, `chore/`). Nothing goes to `main` directly.
- [ ] Decide the kind of change, because it drives the version: a new setting or resource is `feat` (minor); a bug fix is `fix` (patch); a change that only touches tests, docs, CI or internals is `test:`/`docs:`/`ci:`/`refactor:`/`chore:` and does not release by itself.
- [ ] **[R]** Can the change cut management access or break traffic? Then it needs a guard and an `allow_disruptive` argument, and it is designed with the safety notes in the README (*Safety by design*) in mind. Settings that can lock you out of the switch (credentials, reboot, factory reset, config restore, firmware) are deliberately not writable.

## 1. Code

**Client (`internal/escp`)**

- [ ] **[C]** A setting is one file named after it (`ports.go`: types, reader, writer, parser, validation) with its tests in the file of the same name (`ports_test.go`); the file map is in the package overview of `internal/escp/doc.go`. Settings depend on the client and the shared helpers, never on each other.
- [ ] **[C]** The wire layout is written down in [`internal/escp/PROTOCOL.md`](internal/escp/PROTOCOL.md) first (TLV id, byte layout, source: capture, vendor code, or measured), including what is not verified.
- [ ] **[C]** Protocol numbers are named constants with a doc comment (`internal/escp/internal/wire` for TLV ids and sizes); no magic values and no layout bytes outside the client package.
- [ ] **[C]** A reader returns a domain type (not TLVs). A writer validates every argument before anything is sent and returns an error that matches `ErrInvalid`.
- [ ] **[C]** Every writer goes through the write gate with an `Operation{Name, Ports, Impact}`. The impact is the worst the method can do with any arguments (cosmetic, traffic or management).
- [ ] **[C]** A writer reads back and compares when the switch is known to ignore values silently.
- [ ] **[C]** Exported identifiers have doc comments that state preconditions and safety implications. Cancelling the context stops the call before a write.
- [ ] **[C]** A parser of switch replies cannot panic on short, long or malformed data (bounds checked; a fuzz target in `fuzz_test.go` for a new parser).

**Provider (`internal/provider`)**

- [ ] **[R][D]** The method is added to `SwitchClient` (`client.go`); the resource or data source declares the small interface it actually needs.
- [ ] **[R][D]** The type is registered in `provider.go` (`Resources()` / `DataSources()`), with the file named `<name>_resource.go` or `<name>_data_source.go`.
- [ ] **[R][D]** Schema: snake_case names, a `Description` on every attribute, the right mode (Required / Optional+Computed / Computed), the `switch` argument for the multi-switch model (resources: `resourceSwitchAttribute()`), no volatile values in resources (counters belong in data sources).
- [ ] **[R]** Plan-time validation (`ValidateConfig`, validators or `ModifyPlan`) refuses bad values before anything is applied. Apply-time defences stay as a second layer.
- [ ] **[R]** Writes that can touch a port in use or a LAG member go through the guards (`guard.go`, `safety.CheckPorts`), refuse before any write, and honour `allow_disruptive`. Changing only `allow_disruptive` writes nothing.
- [ ] **[R]** The apply reads back and fails loudly ("did not change as requested") when the switch kept something else; state holds what the switch reports.
- [ ] **[R]** Import is supported with a documented id (qualified with the switch name for `switches`); `Read` normalises into the form the configuration uses; drift shows in the plan.
- [ ] **[R]** Destroy semantics are decided and documented: singletons and per-port settings only forget; groups remove their group under the same guard. Restoring a value is always an explicit configuration.
- [ ] **[R][D]** Errors name the attribute (`AddAttributeError`), the cause and the way out. Credentials are `Sensitive` and never logged.

## 2. Tests

Coverage must stay at 100% of reachable statements (`make cover`, exclusions only with `// coverage: ignore - <reason>`). A test that depends on timing is a bug: build the state deterministically. After writing a test, break the code on purpose and confirm the test fails.

- [ ] **[C]** Wire tests with exact bytes against the fake switch (`escptest`): the golden path, a captured vector where one exists, short or truncated replies, boundary values, invalid input (matches `ErrInvalid`), and **nothing sent** when validation or the gate refuses.
- [ ] **[C]** The gate sees the right operation, ports and impact (`write_gate_test.go`); a cancelled context sends nothing.
- [ ] **[R][D]** Terraform lifecycle tests through a real Terraform binary against the in-memory fake client (`provider_test.go`): create, update, import (`ImportStateVerify`, with `ImportStateVerifyIdentifierAttribute` when the id differs from an attribute), drift (change the fake behind Terraform's back, expect a non-empty plan), validation errors (`ExpectError`), destroy semantics, the `switch` argument and qualified ids.
- [ ] **[R]** Guard tests: a refused write performs **zero** writes; an `allow_disruptive`-only change writes nothing; every guard sentence is asserted.
- [ ] **Regression fake** (`escptest/model.go`): the stateful fake stores and answers the new setting, and enforces any rule the switch has (refusals, exclusivity, derived values) **as measured on hardware**. Rules that are only assumed are marked as such in a comment.
- [ ] **Regression scenario** (`internal/regression`): a reader for the setting in `CoreReaders` (so the whole-switch readout includes it) and a scenario that changes it through Terraform, verifies it by reading the switch directly with the client, restores the original by explicit configuration (use `depends_on` and several restore steps where the order matters) and proves the readout is identical. A guard refusal gets a scenario that asserts no write. A scenario takes its ports from the target (`Target.TestPorts`), reads the original values from the switch and never assumes an address, port or initial value, so that it passes on the fake and on a real switch. Run it: `make regression` (fake, also part of `make testacc` in CI).
- [ ] **The scenarios are run, not just compiled.** `make regression` (that is `TF_ACC=1 go test -race -count=1 ./internal/regression/...`; add `-run TestScenarioName` for a single scenario) passes before every push that touches the client, the fake, the provider or a type the scenarios use. Without `TF_ACC=1` every scenario skips, so the pre-commit hook and a plain `go test ./...` say nothing about them. This matters most for refactors: retyping a value (a port list, an id, a rate) can leave everything compiling while a scenario that formats or compares it now builds a different configuration, which is how a retyping once reached CI with four scenarios failing. After such a change, search the scenarios and their helpers for `fmt.Sprint` and `%v` of the retyped values.
- [ ] **Live checks** on a switch you own, never on one you cannot afford to lose: the client's `-tags live` tests (`internal/escp/live_test.go`: unused port, deferred restore before the first write, restore verified) and `make regression-live CONFIRM=1` with a `.test.env` (see `.test.env.example`). Take a configuration backup first, watch pings, and keep the uplink and ports with devices out of it. Not possible for a change? Say so in the pull request.
- [ ] `gofmt`, `go vet` (also `-tags live`), `golangci-lint`, `go test ./... -race -count=1` and `make regression` pass, and so do `make validate-examples` and `make docs-check`.
- [ ] CI is green on every Terraform version of the matrix (the OpenTofu leg is informational).

## 3. Documentation

- [ ] **[R][D]** Provider docs regenerated with `make generate` and committed; `make docs-check` clean. Safety text for a resource lives in `templates/resources/<name>.md.tmpl` (guards, destroy behaviour, ordering with other resources).
- [ ] **[R][D]** An example in `examples/resources/<type>/resource.tf` (or `examples/data-sources/...`) that passes `make validate-examples`, plus `import.sh` for a resource. Documentation addresses and variables only: no real IPs, MAC addresses or credentials.
- [ ] **[R][D]** The README tables are updated: *What you can manage* (resource, web UI page, guard) and, when the verified hardware or firmware changed, *Compatibility*.
- [ ] **[C]** Client documentation is current: doc comments on the exported API, the package overview (`doc.go`), runnable examples (`example_test.go`) for new public entry points, and the data structures a caller sees.
- [ ] **[C]** [`internal/escp/PROTOCOL.md`](internal/escp/PROTOCOL.md) describes the layout and its status (verified live on which firmware, vendor code only, or assumed).
- [ ] **Hardware and firmware compatibility:** the one source is [`internal/escp/safety/compatibility.json`](internal/escp/safety/compatibility.json). Record there which hardware revision and firmware build the change was verified on, and what is only read; then run `make compat-table` to regenerate the README table. The firmware allow-list is read from the same file: a build becomes writable without the opt-in only when a row with `"support": "verified"` names it, and only after it has really been verified on a switch.
- [ ] `CHANGELOG.md`: user-visible changes only, taken from the diff and not from memory; breaking changes and new refusals called out.

## 4. Pull request and release

- [ ] Commit messages and the PR title follow Conventional Commits (lower-case imperative subject, no trailing period). The squash commit takes the PR title.
- [ ] The PR description says what changed, what was verified (and on what), what was not, and the remaining risks.
- [ ] After the squash merge, a release follows [`RELEASING.md`](RELEASING.md): `scripts/release.sh`-style bump of `main.go` and `CHANGELOG.md` on a branch, tag on `main`, push the tag, dispatch `release.yml`, then check the version on the Registry (listing, `terraform init`, the rendered pages).
