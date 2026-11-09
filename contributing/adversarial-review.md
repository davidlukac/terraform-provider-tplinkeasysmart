# Adversarial review of the provider: standing checklists

The [defect classes](defect-classes.md) ask "what went wrong before". This page asks "what would a hostile or careless person do to this design". It adds the provider's questions and threats to the checklists of the library's page, which cover the library underneath and apply here unchanged: [the architecture red team (Part 1), the security red team (Part 2) and how a review runs (Part 3)](https://github.com/davidlukac/escp-go/blob/main/docs/adversarial-review.md). Read that page first; the items below are numbered with a `P` (`AR-P01`, `TM-P01`) so that a review can cite them next to the library's `AR-nn` and `TM-nn`.

It describes classes of threat and the probes to run. It does not list weaknesses: a weakness found by a review is reported privately, see [How findings are reported](https://github.com/davidlukac/escp-go/blob/main/docs/adversarial-review.md#how-findings-are-reported) and [SECURITY.md](../SECURITY.md).

## Part 4. The provider

### Architecture red team, additions

| Id | Question | Fails if |
|---|---|---|
| AR-P01 | **Schema evolvability.** For each attribute and id: can a second model, a filter, a new value or a second switch be added without a state migration? List versus map versus set; enum spellings that come from the library; ids that contain a name the user can change. | a type change, a replacement of live objects or a state rewrite would be needed |
| AR-P02 | **Resource granularity.** Does a rule of the device that spans objects (an ordering, an exclusion) surface as a `depends_on` the user must write? Is it documented as a permanent contract? | the order of a parallel apply decides whether it works |
| AR-P03 | **The meaning of an omitted attribute.** What do omitted, null and empty mean for each attribute, and does destroy forget, delete or reset? | the meaning differs between resources without being written down |
| AR-P04 | **The library underneath.** Does the provider depend on library behavior it does not state or test (a default, a reopen, a removed name)? Does it re-implement a library rule? | a library change would break the provider without a red test in the provider |
| AR-P05 | **The 1.0 gate.** Is there a snapshot of the schema ([#195](https://github.com/davidlukac/terraform-provider-tplinkeasysmart/issues/195)), the deprecation window of the README's Stability section and a plan for state upgrades? | a break could ship unnoticed |

### Threat model: provider

| Id | Threat | Asset | Trust boundary | Defended by | Not defended | Standing probe |
|---|---|---|---|---|---|---|
| TM-P01 | State and plan files read by someone else | credentials, the configuration backup | state backend to reader | sensitive attributes are marked (`password`, the backup content); [SECURITY.md](../SECURITY.md) says to treat state as a secret and to keep it in an encrypted backend | a state file in an unprotected place | search a state and a plan after a full lifecycle for each credential and for backup content |
| TM-P02 | A configuration from an untrusted source (for example a pull request planned in CI) | credentials in the environment | configuration to provider | `host` is refused when it cannot name one switch (a broadcast, multicast or unspecified address, an IPv6 address, a `host:port`) | a configuration that names a host of its own choosing receives the credential | does any pipeline plan or apply a configuration from a source it does not trust, with credentials present? |
| TM-P03 | A saved plan applied later, or state that is stale | the device's management path | plan to apply | guards re-read live state at apply and refuse before any write | a change made by someone else between plan and apply is the device's to arbitrate | apply a plan after changing the device; import then apply |
| TM-P04 | Parallel applies on one device | consistency, the management path | Terraform's parallelism to one device | the library's atomic read-modify-write calls and its one client per switch | device invariants across objects are ordered by the user (`depends_on`) | apply many resources at `-parallelism=10`, in shuffled order |
| TM-P05 | Secrets in provider logs and diagnostics | credentials | provider to log | the log bridge forwards only what the library logs, which carries no values, password or key (`test:TestTflogHandlerResolvesLogValuers` for a value that redacts itself) | a user who prints a variable | run at the most verbose log level through a full lifecycle and search the output |
| TM-P06 | The release and the registry | every user of the provider | repository to registry to user | checksums signed with the release key; a release workflow that runs only when a maintainer starts it; a registry check that installs the release with Terraform and with OpenTofu and warns when the key is close to its expiry | a compromised signing key | rerun the install check; verify the key published where a user can find it |
| TM-P07 | A data source that returns sensitive content | credentials inside a configuration file | device to state | the content is marked sensitive; the data source passes the firmware gate | the content is in state | read it in a plan and a state; confirm it is never printed in a plan |

How it runs is as in Part 3 of the library's page. For the provider, "before every release" includes the hardware regression for a release that changes how a setting is written to a device (see [RELEASING.md](../RELEASING.md)), and a check of the release process against TM-P06.
