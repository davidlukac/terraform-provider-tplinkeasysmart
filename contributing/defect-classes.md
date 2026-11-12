# Defect classes of the provider

The classes of defect that reached a release of this provider, and for each one the question to ask before writing code, the test to write, the check that watches for it, and the probe a hostile or careless configuration would use. They were found by independent reviews, by the hardware regression and by a review of the fixes together (see the issues named in each entry); every one was findable earlier, by a question or a test.

The classes that are not specific to Terraform (documented guarantees, one source per fact, default flips, tests that must fail, flaky tests, support claims, hygiene, the pipeline) are in the [library's catalog](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md) and apply here unchanged: `DC-C01` to `DC-C09`, linked by id below and never copied. The library classes (`DC-L01` to `DC-L08`) matter here only when a change touches the library; a change that needs one is made there first. The provider-only classes, `DC-P01` to `DC-P07`, follow. The security and architecture checklists that go beyond defect classes are in [adversarial-review.md](adversarial-review.md). In the entries, `#N` is an issue or pull request of this repository and `escp-go#N` one of the library.

How to use this page: as the library's page describes it. Before you write code, find your change in the routing table, answer the DESIGN question of each entry it names in the issue or the pull request (a sentence is enough; "not applicable" is an answer), and satisfy their TEST requirements while you write tests. When you review, try the ADVERSARIAL probe of each entry the change touches. When a defect is found, name its class by id in the fix (the pull request template asks for it); if no class fits, add one here in the same pull request, or say why the existing class's check missed.

## Routing: which entries does my change touch?

| My change | Read |
|---|---|
| Any change | [DC-C01](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c01) (promises), [DC-C05](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c05) (tests), [DC-C08](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c08) (hygiene) |
| A new or changed resource | [DC-P01](#dc-p01), [DC-P02](#dc-p02), [DC-P03](#dc-p03), [DC-P05](#dc-p05), [DC-P07](#dc-p07) |
| A new or changed data source | [DC-P01](#dc-p01), [DC-P05](#dc-p05), [DC-P07](#dc-p07), and [DC-P03](#dc-p03) if it sends anything that is not a plain read |
| A new or changed attribute, enum, id or import format | [DC-P05](#dc-p05), [DC-P01](#dc-p01), [DC-P04](#dc-p04), [DC-C03](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c03) |
| Anything that writes to a switch, or decides whether a write may happen | [DC-P03](#dc-p03), [DC-P02](#dc-p02) |
| Several switches, the `switch` argument, provider configuration | [DC-P04](#dc-p04), [DC-P06](#dc-p06) |
| Concurrency, timeouts, retries, unreachable switches, `Configure` | [DC-P06](#dc-p06) |
| A library bump, or a change that needs a library change | [DC-C03](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c03), [DC-C04](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c04), [DC-P07](#dc-p07) |
| Documentation, templates, examples | [DC-C01](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c01), [DC-C02](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c02) |
| A workflow, the release, a dependency, a log line or an error text | [DC-C08](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c08), [DC-C09](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c09) |

## Entry format

Each entry has the same fields, in this order, as in the library's catalog: the rule in one sentence; why (what happened); the DESIGN question; the TEST requirement; AUTOMATION; the ADVERSARIAL probe.

AUTOMATION names each check by kind and name, so that a test can verify that the name exists. The kinds are `test:` (a test function), `fuzz:` (a fuzz target), `make:` (a Makefile target), `lint:` (a linter enabled in `.golangci.yml`), `analyzer:` (a directory of `devtools/`, or the name an analyzer declared there carries), `script:` (a path), `workflow:` (a file in `.github/workflows/`), `reading:` (it cannot be scripted; the reason follows) and `proposed:` (not built yet; the name is followed by the issue that tracks it in parentheses; #196 tracks the ones that have no issue of their own). A claim that a check exists without a name is not accepted. `make lint` runs `devtools/catalogcheck` (`make catalogcheck`, a copy of the library's program), which fails with one line per problem when a name in this page does not resolve (a renamed test, a removed target), when a `reading:` has no reason or a `proposed:` no issue, when a `proposed:` names something that exists already (write it with its kind), when a field of an entry is missing or out of order, when an entry is not in the routing table, and when an id or a link to this page written anywhere else in the repository resolves to nothing; the ids and links into the library's page are checked for their shape only.

---

## How the common classes apply here

| Class | In this repository |
|---|---|
| [DC-C01](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c01) guarantees | The text of a schema description, a template or a generated page is a promise ("destroy only removes it from state"); each needs a test (see DC-P02). `make:docs-check` keeps the generated pages current but does not test what they promise. |
| [DC-C02](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c02) one source | `make:generate`, `make:docs-check`, `test:TestEveryTypeHasItsExample`, `test:TestNoExampleWithoutAType`, `test:TestExamplesPlan`, `test:TestReadmeCompatibilityTable`, `make:compat-table` (the compatibility table is built from the library's file), `analyzer:catalogcheck` (every name this page gives to a check resolves). |
| [DC-C03](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c03) flipped defaults | A library bump runs the whole regression suite (`make:regression`), not only the unit tests, and on a device before a release that changes how a setting is written (`make:regression-live`); a new refusal gets a row in the upgrade guide and in the changelog. The library's default flips (relocation by MAC, a final `Close`) each needed a change here (#188, #191). |
| [DC-C04](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c04) seams | The provider is built and tested against the library candidate before the library's tag is pushed (`make:work` links a local checkout). `reading:` the check is a job of the library's CI, not of this repository's, so no token can name it: the job "Provider against this change" in the library's `.github/workflows/test.yml` (informational) checks out the provider's `main` on every library pull request, links the library under test with a `go.work`, and builds and tests the provider (escp-go pull request #220). |
| [DC-C05](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c05) tests must fail | `make:mutation-diff` for a change of at most 40 lines of non-test Go code; the tests of `internal/provider` take minutes, so mutate a larger change by hand, one file at a time; `analyzer:subtests`. A guard rule has a test for each value of its table (#170). |
| [DC-C06](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c06) flaky tests | `make:testacc` and `make:regression` run with the race detector; the hardware regression waits until the switch answers reads promptly before each scenario (#148). |
| [DC-C07](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c07) support claims | The README compatibility table is generated from the library's `compatibility.json`, which the firmware gate reads too (`test:TestReadmeCompatibilityTable`); the provider states no broader support level. |
| [DC-C08](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c08) hygiene | `test:TestTheTextCarriesDocumentationValuesOnly`, `lint:misspell`, `make:spell`; the `password` argument and the content of the configuration backup are `Sensitive`; the log bridge (`test:TestTflogHandlerResolvesLogValuers`) forwards only what the library logs. |
| [DC-C09](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c09) pipeline | `workflow:test.yml`, `workflow:release.yml`, `workflow:mutation.yml`, `workflow:codeql.yml`, `workflow:registry-check.yml` (installs the release with Terraform and with OpenTofu and checks that both print the release key's id), `make:snapshot` (a dry-run release), `make:actionlint`. |

---

## Provider classes

<a id="dc-p01"></a>
### DC-P01 The plan is what apply does, and state is what the device has

**Rule.** Whatever can be validated at plan time is validated there with the same rule as apply (the library's `Validate`); apply refuses before it writes; after a write the provider reads back and stores what the device reports; a failed apply leaves state truthful; null, empty, unknown and a sentinel each mean one documented thing ("setting nothing is a setting": an omitted attribute leaves the device alone, an empty collection clears it).

**Why.** A disabled MTU uplink read as the port number 1 and an unset mirror as port 0, which a consumer could not tell from a real port (#165). A switch can accept a write and silently keep something else: flow control with a fixed 10 or 100 Mbps speed (0.29.0), an ingress limit beside storm control (0.30.2); the read-back is what notices. The guards that read live state run at apply, not at plan (a destroy plan only warns, #179), so a plan can look clean and the apply stop half way.

**Design question.** For each attribute: its mode, what omitted means, what empty means, what null reads back as, and which plan-time rule refuses a bad value. What does apply do if the write succeeds and the read-back fails or differs?

**Test requirement.** Lifecycle tests with an empty configuration and with the removal of the last element of every collection; plan and apply refuse through one shared function (a plan-time refusal and an apply-time refusal give the same text); a failed update keeps the prior state; a read-back that differs fails loudly; import and then an empty plan.

**Automation.** `test:TestInvalidConfigurationIsRejectedWhilePlanning`, `test:TestValuesUnknownWhilePlanningAreValidatedBeforeAnythingIsWritten`, `test:TestResourcesKeepTheirStateWhenAnUpdateFails`, `test:TestResourcesDetectADeviceThatAcceptsButIgnoresTheWrite`, `test:TestResourcesReportAFailingReadBackAfterTheWrite`, `test:TestResourcesRejectMalformedImportIDs`; the framework's check that the plan after an apply is empty in `resource.UnitTest`; `proposed:TestEveryCollectionAttributeAcceptsEmpty(#196)` (walk the schemas, and for each optional collection run the empty and last-element-removed configurations); `proposed:sentinelscan(#196)` (a schema test that flags a `Default` of `0` or `1` on an attribute whose description says "disabled" or "off").

**Adversarial probe.** Apply, change the device behind the provider's back, plan; apply with the write succeeding and the read-back failing; a configuration with every attribute omitted, null, empty and unknown (computed from another resource).

<a id="dc-p02"></a>
### DC-P02 Create, update, destroy and import are decided contracts

**Rule.** For every resource, what happens on create over an existing object, on update, on destroy (forget, delete or reset) and on import (the id format, what is populated, the plan right after) is decided, written in one table, tested, and never silently overwrites an object or silently leaves a live setting behind.

**Why.** Destroy has three meanings across resources (forget, delete the object, reset a PVID), and for a rate limit, a storm limit and a mirror it leaves the setting active on the switch; the table is in the README (#182). `lag` and `vlan_8021q_group` overwrote an existing object on create (#164). A stored `allow_disruptive` let a removed block act on a port that has a link (#179).

**Design question.** Fill this resource's row of the destroy, create and import table before writing code: what does each operation do to the device, what does it say in the plan, and which of them needs an opt-in?

**Test requirement.** Create over an existing object is refused with an import hint; a forget-only destroy performs zero writes (counted on the fake); a deleting destroy writes what the table says, under the guard; import then plan is empty; a malformed import id is refused with the format in the message.

**Automation.** `test:TestAllowDisruptiveAloneNeverTouchesTheSwitch`, `test:TestResourcesRejectMalformedImportIDs`, `test:TestADestroyPlanWarnsForEveryResourceWhoseDeleteActsOnTheSwitch`, `test:TestEveryTypeHasItsExample` (an `import.sh` for each resource); `proposed:TestDestroyMatchesTheTable(#196)` (the destroy table as data, and a test that each resource's `Delete` behaves as listed, with zero writes for every forget-only resource).

**Adversarial probe.** Apply with the opt-in, remove the block, apply; create over every existing object; import with every id spelling; destroy with `-target`; destroy a plan whose configuration was deleted.

<a id="dc-p03"></a>
### DC-P03 Every write path is guarded, and the opt-in is not a standing permission

**Rule.** Every path that sends a write (a resource, a data source that sends a request that is not a plain read, an import followed by an apply) passes the firmware gate and the port rule before anything is sent; `allow_disruptive` is an instruction for one change, not a permission that outlives its line of configuration; a refusal sends zero requests and says what to do.

**Why.** `loop_prevention` had no guard (#167). The `config_backup` data source sent a request around the firmware gate (#168). An MTU VLAN uplink without a link was accepted (#169). A guard rule changed to tolerate one LAG passed the whole test suite (#170). The port guard exists in four variants (`guardPorts`, `guardLAG`, `guardVLAN` and the 802.1Q `guard`), so a rule is changed in four places.

**Design question.** List every request the new code can send, including a read followed by a write. Which guard covers each? What is the lifetime of the opt-in, and what happens when the block that carried it is removed?

**Test requirement.** For each resource: a refused write performs zero writes (the fake's count); a table over port states (a link, a member of each LAG, idle) and `allow_disruptive`; a change of only the flag writes nothing; a plan sends no write (the destroy-plan warning only reads).

**Automation.** `test:TestAccEveryPortGuardRefusesAnIdleMemberOfAnyLAG`, `test:TestAllowDisruptiveReallyAllowsTheWrite`, `test:TestAllowDisruptiveAloneNeverTouchesTheSwitch`, `test:TestAccRealClientRefusesTheConfigBackupOnUnverifiedFirmware`; `proposed:TestEveryWriteIsGuarded(#196)` (take the library's settings table, list the client methods the provider calls that are writes, and require each to appear in a guard test); `proposed:writecall(#196)` (an analyzer that forbids calling a write method of the client outside the apply helpers).

**Adversarial probe.** A configuration that reaches the uplink through: `count` over the ports data source, a two-step LAG, import then apply, an 802.1Q group, a PVID, a mirror destination, two resources on one port in parallel; an omitted `switch`; an `allow_disruptive` left in state by a block that was removed.

<a id="dc-p04"></a>
### DC-P04 One stable identity, one way to address an object

**Rule.** An object's id, its import id and the way a switch is selected are decided once, frozen, independent of names the user can change, and a configuration with several switches never selects one implicitly.

**Why.** Adding a second `switches` entry turned every object that omitted `switch` into an error, an omitted `switch` sent an object to the provider's own switch, and the id format was not pinned (#181). Renaming a key of `switches` replaces everything on that switch (README, "Several switches").

**Design question.** What is this object's id? Does it contain anything the user can rename? What happens to existing state when a second switch is added or a key is renamed?

**Test requirement.** Golden ids for every way of selecting a switch; adding a switch to the configuration plans no change for existing objects; import ids round trip.

**Automation.** `test:TestTheIdFormatOfEverySelectionIsFrozen`, `test:TestImportIdsCarryTheSwitchName`, `test:TestSwitchSelectionErrorsAreReportedWhilePlanning`, `test:TestChangingTheSwitchReplacesTheResource`; `test:TestSchemaSnapshotIsCurrent` (see DC-P05).

**Adversarial probe.** Rename a key; add a switch; remove the default switch; the same switch under two spellings; `host` in one place and `mac` in another.

<a id="dc-p05"></a>
### DC-P05 The schema is a frozen contract

**Rule.** Attribute names, types, modes, enum spellings, id formats and the shape of data-source collections are consistent across resources and data sources, described, and compared with a snapshot of the schema; removing, renaming or retyping is a break, marked as one, with a deprecation or an upgrade path (see the Stability section of the README).

**Why.** The `qos` resource and data source disagreed on the type of `mode` (#162); the port number was `id` in some data sources and `port` in others (#163); six attributes had no description (#144). Nothing compared the schema of one release with the next until the snapshot of #195.

**Design question.** What is the closest existing attribute, and does the new one have the same name, type, mode and spelling? Will this still be right with a second hardware model or a filter? List, map or set, and why?

**Test requirement.** A snapshot of the schema compared in CI, where a diff is a deliberate edit with a changelog break marker; the naming and description test; enum spellings equal to the library's; volatile data (counters) only in data sources; an upgrade-guide row for every break.

**Automation.** `test:TestEverySchemaAttributeHasADescriptionAndASnakeCaseName`, `make:docs-check`, `make:validate-examples`; `test:TestSchemaSnapshotIsCurrent` (serializes the provider schema and compares it with `internal/provider/testdata/schema_snapshot.txt`), `test:TestSchemaSnapshotCoversEveryResourceAndDataSource`; `reading:` whether a new attribute is consistent with its neighbors needs a person.

**Adversarial probe.** Write the upgrade-guide row for the change; if you cannot say how a user migrates, it is a break. Express the same configuration against the previous and the new schema.

<a id="dc-p06"></a>
### DC-P06 Failure is fast, bounded and visible

**Rule.** A silent or failing switch costs one timeout per run, not one per resource; parallel applies on one switch cannot undo each other or fail depending on scheduling; clients are closed when the provider is configured again; a refusal names the attribute and the way out.

**Why.** With an unreachable switch a refresh of 15 resources cost minutes (#180). The clients of a previous configuration were never closed, which the library's one-client rule turned into a failure (#151). `timeout_seconds` had no upper bound at plan time (#152). An ingress limit and storm control exclude each other on one port, so two resources applied in parallel could be refused depending on order (the README documents the `depends_on` that orders them).

**Design question.** What happens with forty resources and the device switched off? With `-parallelism=10` and two resources that touch the same device state?

**Test requirement.** A silent fake with a counted timeout: N resources cost one timeout; a parallel apply of resources that share device state ends in the same result in every order; configuring the provider twice closes the first clients.

**Automation.** `test:TestFailFastStopsAskingASwitchThatDidNotAnswer`, `test:TestFailFastNeverLatchesASwitchThatAnswered`, `test:TestReconfiguringTheProviderClosesThePreviousClients`, `analyzer:lockwait` (flags a blocking call while a mutex is held; an intended wait carries `// lockwait: <reason>`), `analyzer:readwrite` (flags a function that reads a setting of the switch and then writes one; an intended case carries `// readwrite: <reason>`); `proposed:TestParallelAppliesAreOrderIndependent(#196)` (a shuffled-order apply of resources that share device state); `proposed:latencyfake(#196)` (a fake with injected latency for the lifecycle tests).

**Adversarial probe.** Turn the switch off during a plan; apply twenty resources on one switch at `-parallelism=10`; interrupt an apply; change the provider configuration between two plans.

<a id="dc-p07"></a>
### DC-P07 The test matrix is complete, and the hardware has seen it

**Rule.** Every resource and data source has lifecycle tests against the fake (create, update, import, drift, destroy semantics, validation errors, the guard, the empty value) and a regression scenario that is run, not only compiled, on a real switch before a release that changes how a setting is written; a discovery that only a device can make becomes a rule of the fake or a scenario.

**Why.** The MTU VLAN write had never been exercised on a device before the 0.34.0 run (changelog). Without `TF_ACC=1` every scenario skips and still reports `ok`. A retyping of a value left every scenario compiling while a few built a different configuration (`CONTRIBUTING.md`, section 2). One scenario relied on a default that had flipped and failed on the device (0.34.0, #191).

**Design question.** Which rows of the matrix does this change add? Which of them can only be proven on a device, and who runs them?

**Test requirement.** The matrix in `CONTRIBUTING.md`, as tests; a scenario takes its ports from the target and restores the original value by configuration; `make regression` passes before a push that touches the client, the fake, the provider or a type the scenarios use; a rule that only a device showed is added to the fake as measured, and a rule that is only assumed is marked as such in a comment.

**Automation.** `make:testacc`, `make:regression`, `make:regression-live` (maintainers; needs hardware), `fuzz:FuzzParseEnvFile` and `make:fuzz` (the regression harness's parser of the credentials file takes arbitrary text); `proposed:TestEveryTypeHasARegressionScenario(#196)` (list the registered types and require a scenario name for each); `proposed:scenarioskip(#196)` (CI fails when a regression scenario was skipped).

**Adversarial probe.** Run a scenario with the device slow and with it dropping replies; retype a value and search the scenarios for `fmt.Sprint` and `%v` of it.
