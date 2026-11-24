# Changelog

## 0.35.0

A minor version because it adds two data sources (`tplinkeasysmart_led`, `tplinkeasysmart_loop_prevention`) and because a silent switch now behaves the way the 0.34.0 notes already said, which changes what a plan against a switch that is off costs and says (the row below); no attribute, id or default of an existing resource or data source changes, so there is no state step. The provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.11.0 (0.34.0 had v0.9.0). The [upgrade guide](docs/guides/upgrading-to-0.35.md) has the same notes for a configuration.

- **Behavior change**, one row (the numbers are the issues):

  | 0.34.0 | 0.35.0 |
  |---|---|
  | The 0.34.0 notes said that after the first request to a switch that gets no answer every later request fails at once (#180). That was not active: the latch existed, but nothing used it, so every request to a silent switch cost the full timeout with its retries (#219) | After the first request to a switch that gets no answer, while nothing has answered, every other request to that switch in the same run fails at once, a request that was already waiting for its turn included (#219, #222). The error says that the request was not sent and whether the first request without an answer was a read or a write; it never says that a write may have been applied. The first request keeps its own error (a write that was sent and got no reply: "may have been applied") (#224). Each switch has its own latch, and the latch lasts for one walk of Terraform (a plan, an apply). A switch is latched only while none of the requests the provider itself sent to it has been answered; what the library client asks inside a write does not count, so a switch that answered only that and then ignores the write is latched by it (README, "Safety by design") |

- **Added:** the data sources `tplinkeasysmart_led` and `tplinkeasysmart_loop_prevention` (`enabled`, `id` and the `switch` argument; read-only), to read the value of these two settings before managing them or on a switch that is not managed (#213).
- **Fix:** the latch of the row above, wired into the provider (#219), made to refuse the requests that are already queued behind the first one (#222) and to say, in a refusal, that the request was not sent (#224). With twelve data sources on a silent switch the provider now sends the datagrams of one request and refuses the other eleven; in 0.34.0 each paid the timeout (#219, #222, #224).
- **Fix (library v0.10.0 to v0.11.0; the bumps were dependency bumps without an issue, PRs #208, #210 and #236):** the window in which a stale reply left in the client's socket could be read as the answer to the next request on a loaded machine is narrowed, not closed (v0.10.0); a request longer than the wire can carry is refused instead of being sent with a wrapped length, and a call canceled while the socket is drained sends nothing (v0.10.1); a management write whose context ended before any datagram was sent, and a read or key exchange before it that got no answer, no longer wait for a switch that was never written to: a `management_ip` apply that is interrupted before its write was sent can now end with the context's error at once, where it waited and reported that the switch did not answer at its new address (v0.11.0); and a `management_ip` apply whose read or key exchange before the write gets no answer ends with the library's plain "no reply" error, where it waited (a narrow window, because the provider's own read of the switch comes first). The provider still adds "The switch may now be at the old or the new address; look at both before applying again" to these errors, which is false for a write that was never sent: that text is to be corrected in a later version, so do not read the new error as a sign that the switch moved. The provider checked the library's behavior changes of these three versions against its own code and found none that changes what a configuration does, except the errors of an interrupted `management_ip` apply; the library's changelog has the details.
- **Build:** the release is built with the Go version of `go.mod`, 1.27.4, which fixes GO-2026-6859 in `net/http` (a toolchain bump, PR #201, without an issue).
- **Maintainers:** `registry-check.yml` no longer starts itself when the Release workflow ends (zizmor reports the `workflow_run` trigger as dangerous, #217); after a release start it with `gh workflow run registry-check.yml -f version=X.Y.Z` (RELEASING.md); it still runs weekly.
- **Internal, no change in behavior of any setting:** `clientFrom` and eight write-only `sw` fields are removed, and `make deadcode` (part of `make lint`) reports code that only a test uses (#218); the pull request template has a "Merge readiness" section and a job of the same name checks it (#220); a zizmor gate audits the workflows (#217); the regression safety net restores the LED, IGMP snooping and loop prevention a failed scenario left changed (#211, #215); the analyzers, layering rules and a schema snapshot of the library, a check that every automation name of the defect catalog exists, a weekly vulnerability scan, module checks and the provider's defect classes and adversarial review are added (#193, #194, #195, #199, #202).
- **Verified on hardware:** the full regression on a real switch (a TL-SG108E, firmware 20250710) of 2026-11-23, on commit e18f20c: 101 passed, 0 failed, 7 skipped, the configuration hash back at its baseline (drift clean); no ping was lost to the router or to the other switches of the test network, and 41 of 2274 were lost to the switch under test while its address moved (expected). That commit contains the latch (#219, #222, #224) and the library v0.11.0; the provider binary of this release is built from the same Go code, except for a comment in the latch's file (the Go files that differ are that comment, the zizmor gate programs under `tools/`, which are not part of the provider, and the test-support code of the regression suite, which is not either; the rest is the hooks, the workflows and documentation). What it covers: the latch's wrapper and the order of the firmware gate on a real client and a switch that answers (never latched), and the library bump. What it does not cover: a switch that does not answer (the latch was tested against the fake switch only, through real Terraform), the new data sources `tplinkeasysmart_led` and `tplinkeasysmart_loop_prevention` (no hardware scenario reads them; the regression uses the two settings as resources), the one scenario of the not-found kind of silence (`TestScenarioAMACNobodyHasIsNotFound`, one of the 7 skipped), and a switch that answers the client's internal requests and ignores a write.

## 0.34.0

It makes the schema and the behavior of a few settings consistent before the interface is frozen at 1.0, so it **breaks** several things. (More minor versions may follow before 1.0: the version number says nothing about how close 1.0 is.) Every change has a step in the [upgrade guide](docs/guides/upgrading-to-0.34.md); none needs a state rewrite. The provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.9.0.

- **Breaking changes**, one row each (the number is the issue):

  | 0.33.0 | 0.34.0 |
  |---|---|
  | `data.tplinkeasysmart_qos` had `mode` as a number and `mode_name` (#162) | `mode` is the name (`port-based`, `802.1p`, `dscp-802.1p`), `mode_name` is gone |
  | `tplinkeasysmart_ports` and `tplinkeasysmart_port_statistics` named the port number `id`, and the statistics the link state `link` (#163) | `ports[].port`, and `ports[].actual_speed` in the statistics; the `id` of a LAG or VLAN element is unchanged |
  | `tplinkeasysmart_lag` and `tplinkeasysmart_vlan_8021q_group` overwrote an existing group or VLAN on create (#164) | create is refused with an import hint when it exists (VLAN 1 always does); objects already in state are unaffected |
  | `vlan_mtu.uplink_port` was `1` when the MTU VLAN is off, `port_mirror` reported `mirror_port = 0` when off (#165) | both are `null`; the resource attribute `mirror_port` is optional without a default and refuses 0, so remove `mirror_port = 0` |
  | `multicast_groups` was an attribute of the `igmp_snooping` resource (#166) | removed there; use the new data source `tplinkeasysmart_igmp_snooping` |
  | `tplinkeasysmart_loop_prevention` had no guard and wrote even the value the switch had (#167) | changing the value needs `allow_disruptive = true`, applying the same value writes nothing |
  | `data.tplinkeasysmart_config_backup` ran on any firmware build (#168) | passes the firmware gate: refused on an unverified build unless `allow_unverified_firmware = true` |
  | `tplinkeasysmart_vlan_mode` accepted an MTU uplink without a link (#169) | refused, even with `allow_disruptive`, unless the uplink has a link and is in no LAG |
  | An object or data source without `switch` went to the provider's own switch or to the only named switch (#181) | with a `switches` map every object must set `switch`; `switch = "default"` is the provider's own switch (reserved, not a map key); ids are unchanged and now frozen, import with a `default:` or `<name>:` prefix |
  | A client with `host` and `mac` looked the switch up by its MAC when `host` did not answer (library #144) | it keeps to `host` unless `relocate_by_mac = true` (new, at the provider, per `switches` entry or `TPLINK_EASYSMART_RELOCATE_BY_MAC`); needs `mac` (#191) |
  | A silent switch cost the whole timeout and its retries on every request (#180) | the first request that gets no answer from a switch that never answered fails every later request to it at once |

- **Added:** `relocate_by_mac` (provider and per entry), the data source `tplinkeasysmart_igmp_snooping`, a warning on a destroy plan when a stored `allow_disruptive` lets a removed LAG, VLAN group or PVID act on a port that has a link up (#179), the guide and the Registry index page (requirements, safety, stability) and a Stability section in the README (#182).
- **Fix (library v0.9.0):** a write that got no reply is reported as possibly applied and is never sent twice; management writes first prove the session; `Close` is final and ends calls in flight; a second client of one switch is refused by address or by MAC; read-modify-write calls are atomic; the 20230218 compatibility row says exactly what was verified on that build. The library's changelog has the details.
- **Internal:** the regression harness dials a new client when it takes the switch back (the library's `Close` is final) (#188).
- **Verified on hardware:** the full regression on a TL-SG108E with firmware 20250710 on the candidate (library v0.9.0), with its disruptive scenarios (the management address moved and back, DHCP on and off, a speed change, a port taken down, a LAG, a mirror destination and a VLAN group on device ports, `allow_disruptive` writes to a device port): 86 passed, 1 failed, 1 skipped; the failure was the stale-host scenario, which relied on the old relocation default and now sets `relocate_by_mac` (and a new scenario checks that it is off by default), both passed on the switch; the MTU VLAN write, which no scenario had written before (uplink on the linked port, then back to the original mode), passed. 0 of 2,205 pings lost to the router and to the other switch; the configuration hash of the test switch was back at its baseline and every Terraform plan clean afterwards. The 11 data sources of the module and the new `igmp_snooping` data source read on both switches, and state files written by 0.33.0 refresh without changes to resources.

## 0.33.0

- **Behavior changes** (no setting is added or removed; three things that were accepted before are refused or done differently now, so this is a minor version):

  | 0.32.7 | 0.33.0 |
  |---|---|
  | `timeout_seconds` (provider level and per entry of `switches`) required at least 1; the library took any value, so `31` or `3600` passed `validate` and plan and held a call for that long when a switch stayed silent (#152) | the attribute takes **1 to 30** (the library's `MaxTimeout`): a larger value is refused at plan time with the attribute named, before any client is built. The description says so. A configuration that set more than 30 lowers it |
  | The provider never closed the clients it built, and Terraform configures it again for every plan and apply in one process, so each configuration left its sockets open (#151) | the clients of the previous configuration are closed before the new ones are built. This is also what the library's new one-client rule needs (the next row) |
  | Any number of clients of one switch could exist in a process, and they failed each other with timeouts and rejected writes (escp-go #115, via #154) | a second client of the same switch in one process fails at once ("another client of this process already talks to that switch"): in a provider block that means two `switches` entries that reach one switch. Name each switch once |

- **Fix (library v0.8.0, #154):** the provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.8.0. A client that knows its switch's MAC (the `mac` setting, or the one of its first contact) refuses a reply that carries no MAC at all, as it refuses one with another MAC ("the device that answered is not the configured switch"), and nothing is written; real switches put their MAC in every reply (captured on both firmware builds). The firmware check (`allow_unverified_firmware`) remembers the verified build per switch, by its MAC, and no longer per gate, so a switch that comes after another one with a different build is checked on its own.
- **Internal, no other change in behavior:** the values moved from `escp` to `escp/model` in the library, so every `escp.PortID` and friends is `model.PortID` here; `SwitchClient` has `SwitchMAC` (the firmware gate keys by it). The regression suite checks the switch behind the provider's back with clients of its own, which the one-client rule refused (32 of 79 scenarios on the hardware, 20 on the fake in CI): its clients step aside while the provider has the switch and take it back before every check, at the end of a case and in the safety net.
- **Verified on hardware:** the full regression on a TL-SG108E on Terraform 1.17.1, with its disruptive scenarios (the management address moved and back, DHCP on and off, a speed change, a port taken down, a LAG, a mirror destination and a VLAN group on device ports, an `allow_disruptive` write to a device port): 83 passed, 0 failed, 1 skipped by design (a MAC that nobody has needs a network to search); the configuration files of both test switches read byte for byte as before and every Terraform plan was clean; no ping was lost to the router or the other switch. The first run, before the harness change above, failed 32 scenarios with the one-client error and left the switch as found.

## 0.32.7

- **Fix:** the provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.7.0, which closes four gaps in what reaches the switch. A provider block or `switches` entry that names the switch by `host` only now keeps to the switch it first met: if another device takes the address over later (a lease that changed hands) during the run, the request is refused ("the device that answered is not the configured switch") and nothing, the password included, is sent to the stranger; setting `mac` still pins the switch from the start. A `host` that resolves to a broadcast, multicast or unspecified address, written as `4294967295` or `broadcasthost`, for example, is refused when the provider is configured, as the dotted `255.255.255.255` always was. `mac = "00:00:00:00:00:00"`, which pinned nothing, is refused by the argument's validator. An interrupted run (Ctrl-C, a deadline) no longer waits for the calls queued ahead of it on the same switch.
- **Documentation:** six list attributes of five data sources (`ports` of `port_statistics`, `bandwidth_control` and `storm_control`, `vlan_port_based.vlans`, `vlan_8021q.vlans` and `.pvids`) had no description and showed only a type on their generated pages; they are described now (#144). A test now walks every schema and fails on an attribute without a description or whose name is not snake_case.
- **Internal, no change in behavior of any setting:** every case of a table-style test runs as a subtest and `make lint` fails on a case loop that does not (#142); `make spell` checks American English in every tracked text file before the commit and in lint (#146); the hardware regression waits until the switch answers reads promptly before each scenario, because after the disruptive scenarios (device ports taken down and up) it stayed slow for several seconds and the next scenario failed (#148). The full hardware regression on a real switch passed on this code, with the disruptive scenarios on the device ports, the management move and the DHCP lease: 78 passed, 0 failed, 1 skipped by design, the configuration files of both test switches read byte for byte as before, no ping lost to the router or switch 01.

## 0.32.6

- Internal, no change in behavior of any setting: the provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.6.0. Of the library's changes only one reaches code of this repository, and only its regression-suite harness: the port lists of the suite's configuration (test ports, protected ports and the disruptive ones) are read with `ParsePortID`, which now accepts only the canonical decimal spelling, so `"+3"` or `"03"` in such a list is refused. The provider's own settings never went through it. The full hardware regression on a real switch passed on this code (68 passed, 0 failed, 7 opt-in skips; the configuration files of both test switches read byte for byte as before).

## 0.32.5

- Internal, no change in behavior of any setting: the provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.5.0 (a socket that fails is reported as that error and not as "no reply" after the retries, and another device answering during a configuration backup is reported as the wrong switch). The frozen copy of the library inside the provider is deleted, the stricter lint set of the library is adopted (the code it found is fixed; among it, a `port_priorities` value of `tplinkeasysmart_qos` that is not a number is reported as "Invalid priority" instead of relying on an unchecked type assertion), and mutation testing runs in CI for the lines a pull request changes. The full hardware regression on a real switch passed on this code (68 passed, 0 failed, 7 opt-in skips; the configuration files of both test switches read byte for byte as before).

## 0.32.4

- **Fix:** the `host` setting (provider or switch) is refused when the provider is configured if it cannot name one switch: the unspecified address, a multicast group, `255.255.255.255`, an IPv6 address, `host:port` or a name with a space ("Cannot configure switch client: … does not name one switch"). Every write carries the admin password, so such an address would have sent it, with the write, to every device on the segment. The check is in the library ([escp-go](https://github.com/davidlukac/escp-go) v0.4.0, which the provider now uses); a `mac` setting, a name or a normal address is unaffected. Internal, no other change in behavior: the provider follows the library's renames (the full hardware regression on a real switch passed on Terraform 1.17.1: 67 passed, 0 failed, 7 opt-in skips, the configuration files of both test switches read byte for byte as before, no ping lost to the router or switch 01), the code and docs use American English, and CI tests Terraform 1.4, 1.6, 1.8, 1.10, 1.12, 1.14, 1.16 and the latest release, and tries the newest pre-release.

## 0.32.3

- Internal, no change in behaviour: the provider uses [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.3.0 (the library renamed fields and methods before 1.0, and now refuses an unusable client configuration, a refused read and a bad value earlier and in a fixed order; the full hardware regression on a real switch passed: 67 passed, 0 failed, 7 opt-in skips, the configuration files of both test switches read byte for byte as before, no ping lost to the router or switch 01).

## 0.32.2

- Internal, no change in behaviour: the provider uses the extracted ESCP library, [github.com/davidlukac/escp-go](https://github.com/davidlukac/escp-go) v0.2.0, instead of its internal copy (the full hardware regression on a real switch passed, and the configuration files of both test switches read byte for byte as before). The one visible difference is in the debug log (`TF_LOG=DEBUG`): the 802.1Q VLAN writes are named `SetDot1QVLAN` and `DeleteDot1QVLAN` (were `SetVLAN8021Q` and `DeleteVLAN8021Q`).

## 0.32.1

- **Fix:** `tplinkeasysmart_vlan_8021q_group` refuses to add a VLAN to a full table before it writes anything. The switch holds at most 32 802.1Q VLANs, VLAN 1 included (probed on a TL-SG108E: 31 more were accepted, the next was refused with an unexplained error code 7505); the error now says "the 802.1Q VLAN table is full: the switch holds at most 32 VLANs (VLAN 1 among them) and has 32, so VLAN 20 cannot be added". Changing a VLAN that is already in the table is not an addition and still works.
- **Fix:** `tplinkeasysmart_management_ip` stops at once when a static address is not a dotted IPv4 address at apply time (plan-time validation refuses it first, so this is the second layer). It used to report it and carry on, and with `allow_disruptive` ended with a misleading "the switch may now be at the old or the new address".
- **Dependency:** `golang.org/x/net` v0.62.0, which fixes GO-2026-6866 and GO-2026-6860. The provider does not call the affected code; the scan exited clean before, and the module no longer carries them.
- Internal, no change for users: the coverage policy and the badge are a Go tool, the CI linter and vulnerability scanner are pinned, the CI runs on a fixed Ubuntu image.

## 0.32.0

- **New:** `allow_unverified_firmware` also comes from the environment variable `TPLINK_EASYSMART_ALLOW_UNVERIFIED_FIRMWARE`, for configurations that take everything from the environment. The argument wins over the variable and a switch entry over both; a value that is not a boolean stops the run before anything is written.
- **Fix, behaviour change:** a port-based VLAN group is checked against the switch before anything is written. An id above what the switch holds (the larger of the group count it reports and its number of ports, so larger models of the family are not refused) is refused, even with `allow_disruptive`. Creating a group that already exists is now an error that points to `terraform import`, and two resources for the same id no longer overwrite each other. A configuration that relied on either will fail at apply.
- **Fix:** a reply left in the client's socket could be taken for the answer to the next request. A switch answers every request to UDP port 29809 whoever asked, and a client's own discovery (the lookups by MAC, `Info`) drew its sequence numbers from the same range as ordinary requests, so a discovery reply meant for one client could sit in another's socket until its counter reached the same number and then be read as the answer to a read (a LED, port mirror or LAG read came back with a discovery reply's contents). A client's discovery now uses its own range (1000-1999), and a client discards what is waiting in its socket before it sends. Seen in full runs of the hardware regression, where several clients look up one switch; it needs more than one client in a process.
- Internal: the group limit is a value object (`PortVLANLimit`), the fake switch never hands out a UDP port twice, and the library's limits are compared with the ones the web UI enforces in a test. No behaviour change.

## 0.31.0

- **New: a debug log of what the client does.** `TF_LOG=DEBUG terraform plan` now shows each request and reply the provider exchanges with a switch (operation, sequence number, the types and sizes of the values, never the values), the sessions it opens, the lookups by MAC, the writes it sends with their impact, and the lease and address-change steps; a lost packet or a repeated download shows as a warning. Every line carries `component=escp`, `host` and `mac`, and is attributed to the resource operation that caused it. No password, user name or session key reaches the log (tested). The library takes a `*slog.Logger` in `Config.Logger` and `DiscoverOptions.Logger` (nil discards), so a program that embeds it gets the lines in its own format. **Fix:** a late discovery reply can no longer be mistaken for a client's reply: discovery probes now draw their sequence numbers from 1000-1999 and clients from 0-999 (a rare lost-or-wrong answer when a discovery and a request ran close together). Internal: the regression suite puts the VLAN mode and the mirror back when a scenario fails halfway, asks again after an address change, prints the library trace of a failed scenario, and a test checks that every resource and data source has an example that plans.

## 0.30.3

- **Fix:** a configuration backup is complete and verified, or it fails. The switch sends the file in UDP packets that carry no index, total or checksum, so a lost packet used to give a shorter file that looked valid. The download (the `tplinkeasysmart_config_backup` data source and the library's `ConfigBackup`) is now accepted only when it starts with the configuration magic, its packets are the expected lengths, and a second, separate download is byte for byte the same; otherwise it is repeated (up to the configured retries) and then fails with an error that says why. A rejection, a missing reply and a cancelled run still end the call at once. Internal: `tools/configbackup`, a small command that writes a verified backup file, and the documentation of when to run the Terraform scenarios.

## 0.30.2

- **Fix:** an ingress bandwidth limit and storm control on the same port are no longer written together. The switch keeps only one of them per port and turns the other off without a word (probed on firmware 20230218 and 20250710), so a configuration with both never converged: every apply undid the other and the next refresh showed one gone. `tplinkeasysmart_bandwidth_limit` with an `ingress_kbps` other than 0 is now refused while storm control is on for the port, and `tplinkeasysmart_storm_limit` with a traffic type is refused while an ingress limit is set, before anything is written; the error names the port and what to do. To change over, turn the first off and order the applies with `depends_on`. The egress limit, an ingress limit of 0 and storm control switched off are unaffected. A configuration that set both on one port now fails at apply instead of flapping.

## 0.30.1

- No change in behaviour; the library behind the provider now uses typed values (value objects) for what used to be plain numbers and strings. Visible only in texts: port lists in messages read `2,5` instead of `[2 5]`; a bad device description, VLAN name or switch name is refused with one sentence each ("Invalid device description", "Invalid VLAN name", "Invalid switch name") that says what is allowed. Library API (`internal/escp`, not a Terraform interface): `Config.Password` is an `escp.Password` that prints as `[redacted]`, `ConfigBackup` returns an `escp.ConfigFile`, `FirmwareError` carries the `escp.Firmware` instead of `Version` and `Build`, `Config.BroadcastAddr` that is not an IPv4 address is refused, result codes are `escp.ReplyCode`, and addresses, ports, VLAN ids, build dates and TLV types have their own types.

## 0.30.0

- **New: name a switch by its MAC address.** `mac` on the provider block and on each entry of `switches` (environment variable `TPLINK_EASYSMART_MAC`; `aa:bb:cc:dd:ee:ff`, colons or hyphens, any case, checked while planning). The MAC pins the identity: a device with another MAC at `host` is refused before anything is written to it (credentials ride on every write, so a reused address would otherwise receive them). Without `host`, or when `host` does not answer, the switch is found by a layer-2 discovery on the first request (about half a second; a lookup that finds nobody is repeated once). `host` is optional when `mac` is set; an entry of `switches` needs one of the two. The same host or MAC cannot be configured twice. The lookup needs the switch on the same layer-2 segment as the machine running Terraform; the MAC is a claim, not a credential, so it protects against accidents (a reused or changed address), not against an attacker on the segment.
- **New: `tplinkeasysmart_management_ip` can lease the address from a DHCP server.** `dhcp = true` makes the switch ask for a lease; `ip_address`, `netmask` and `gateway` then report the lease and must not be set. With `dhcp = false` (the default) the three are required as before, and a switch that leases an address is moved back to the static one. Changing between the two needs `allow_disruptive`; turning DHCP on needs `mac` in the provider configuration, because the leased address is not known in advance and only a lookup by MAC finds the switch again. Verified on firmware 20250710 (about four seconds to a lease; a static address written while leasing is ignored). Upgrading: existing state has no `dhcp`, which reads as `false`, so a configuration without it plans no change.
- **Behaviour change:** writing a static address now writes the DHCP flag off with it (the switch ignores a static address while the flag is on). Before, a switch that leased its address was refused with "turn DHCP off in the web UI first"; now `dhcp = false` with the three addresses moves it, with `allow_disruptive`.
- **Fix:** a VLAN mode changed outside Terraform no longer breaks the refresh of the whole configuration. `tplinkeasysmart_vlan_8021q_group`, `_vlan_8021q_pvid` and `_vlan_port_based_group` warn on read and destroy when their mode is not active (the state is kept, changes stay refused); before, the refresh failed for every resource.
- The refusals for a VLAN feature that is not active now say what was measured: the switch accepts such a write but does not keep it, and resets the table when the mode is switched on, so set the mode first.
- The system resource's description points to `tplinkeasysmart_management_ip` for the address and DHCP; the documentation no longer says DHCP is not writable.
- Internal: the ESCP library has one MAC value type; regression scenarios for MAC and DHCP (the DHCP and move-by-MAC scenarios run on hardware only); contributing checklist, `internal/udp`, files split by concern. No other change in behaviour.

## 0.29.1

- Documentation: the 802.1Q VLAN group and PVID pages say that a configuration that also sets the VLAN mode needs `depends_on` on `tplinkeasysmart_vlan_mode` (and how to remove a PVID and its VLAN), and the group example shows it. Internal: an end-to-end regression suite (real Terraform and provider against a stateful fake switch, run in CI) and an exported test constructor, `provider.NewWithDialer`. No change in behaviour.

## 0.29.0

- `tplinkeasysmart_port`: `flow_control = true` with a 10 or 100 Mbps speed is refused while planning, and again by the client before anything is sent. The switch only keeps flow control with `auto` or `1000Mbps-full`; before, it accepted the speed and left flow control off, so the apply changed the speed and then failed on read-back.
- `tplinkeasysmart_management_ip`: a lost reply to the change no longer reports a failed key exchange for a move that happened. The provider probes the switch (by its MAC address): the apply succeeds when the switch has the new settings, says "not applied" when it is unchanged, and otherwise names what it found (nothing, a different device, or settings that are neither the old nor the new ones). A cancelled run (Ctrl-C) during the change is settled the same way.
- **Fix:** changing only `allow_disruptive` on `tplinkeasysmart_port`, `_bandwidth_limit`, `_storm_limit`, `_lag`, `_vlan_port_based_group`, `_vlan_8021q_group`, `_vlan_8021q_pvid` or `_port_mirror` no longer writes to the switch (it used to rewrite the port or LAG with identical values), and removing it is no longer refused by the guard.
- `tplinkeasysmart_lag` and `tplinkeasysmart_port`: when the switch refuses a LAG or a half-duplex port, the error names the cause (error codes 7405 to 7418: members with different priorities, speeds, flow control or admin state, auto-negotiation mixed with a fixed speed, half duplex on a member). The descriptions say what the switch checks when a group is created and that it does not keep the members alike afterwards.
- A cancelled run stops sending: nothing is written once the context is done, and a write waiting for a reply returns at once.
- Firmware guard: the build in the version string must stand alone ("Build 2025071012" was read as the verified build 20250710).
- **Behaviour changes:**
  - A port that has a link up and is a LAG member (the uplink) is reported as "has a link up" by every port guard (`port`, `bandwidth_limit`, `storm_limit`, the `port_mirror` destination, `vlan_8021q_group`, `vlan_8021q_pvid`). The refusal is the same; the sentence differs.
  - A write that names a port the switch does not have (a mirror source port, the MTU VLAN uplink) is refused before anything is sent. The client reads the switch's port count once for that, so the first such write costs one extra read; if the switch does not report it, those writes fail.
  - `tplinkeasysmart_vlan_mode`: the "did not change as requested" error formats its values differently.
- The ESCP client is now a self-contained library inside the provider (typed values, `ErrInvalid`, a write gate that is told the operation and its impact, a `safety` package with firmware, port and dry-run gates, a fake switch for tests) in preparation for a command line client. It changes nothing for provider users beyond the items above.
- Building from source needs Go 1.27.3.

## 0.28.0

- tplinkeasysmart_management_ip: set the static IP address, subnet mask and gateway (allow_disruptive, plan-time validation, the client follows the switch); verified live on firmware 20250710

## 0.27.0

- VLAN writes, verified on a real switch (TL-SG108E v6, firmware 20250710, free ports only): `tplinkeasysmart_vlan_mode` (MTU / port-based / 802.1Q, exactly one active), `tplinkeasysmart_vlan_8021q_group` (id, name, tagged and untagged ports) and `tplinkeasysmart_vlan_8021q_pvid`. Changing the mode needs `allow_disruptive = true`; groups and PVIDs refuse ports that carry a link or belong to a LAG, and a change that would leave a port in no VLAN. 802.1Q must be the active mode for groups and PVIDs.
- Firmware guard: writes are refused on a firmware build the provider was not verified against (20230218 and 20250710 are); reads and imports always work. `allow_unverified_firmware = true` (provider, or per switch in the `switches` map) lifts it.
- A rejected write now reads `switch rejected the request (error code N)` instead of claiming bad credentials; `errors.Is(err, escp.ErrAuth)` still matches and `*escp.RejectedError` carries the code.
- `tplinkeasysmart_lag` knows the switch's real rules: group 1 takes ports 1-4 and group 2 takes ports 5-8, with two to four members. A group or port set that breaks them is refused while planning and again by the client before anything is sent. A LAG the switch itself refuses (a mirrored or mirror-destination port, a port in a port-based VLAN other than the default, a bandwidth limit, storm control) now reports the conflict instead of blaming the credentials. LAG creation was verified on firmware 20250710 as well, and the DESTROY path through ESCP. Mirror egress and unknown-unicast storm control, which were not covered before, were verified on it too.
- **Behaviour change:** a LAG group other than 1 or 2, or with fewer than two members, is now refused at plan time (the switch never accepted them).
- Live tests take their unused ports from `TPLINK_EASYSMART_LIVE_PORTS` / `TPLINK_EASYSMART_LIVE_SOURCE`, plus an opt-in 802.1Q mode test.

## 0.26.3

- Documentation only: the README states the verified device (TL-SG108E v6, firmware 1.0.0 Build 20230218) and the lockout caveat up front, Compatibility is a model / hardware / firmware / status table (firmware before the 2023 RSA key exchange is stated as not supported), and the hardware report template starts from a read-only configuration. No behaviour change.

## 0.26.2

- Documentation only: the README, RELEASING.md and workflow notes now describe the provider as published on the Terraform Registry (install with `terraform init`, version pin, building from source moved to Development). The GitHub release notes are applied from this changelog by the release workflow. No behaviour change.

## 0.26.1

- A fragmented reply (configuration backup) is capped at 64 KiB: a sender that never clears the fragment flag now gets `ErrReplyTooLarge` instead of growing the buffer until the deadline.
- Repository readiness for publication: SECURITY.md, issue and pull request templates, pre-commit hook, measured coverage badge.

## 0.26.0

- Several switches in one provider: a `switches` map (name to host and credentials) plus an optional `switch` argument on every resource and data source; provider aliases keep working and can be mixed in. Ids and import ids of a named switch are prefixed (`sw02:led`, `sw02:7`); the provider's own switch keeps the plain ids. Changing `switch` replaces the object; duplicate hosts are rejected. Client deadline race fixed. See `examples/multi-switch`.

## 0.25.0

- Several switches at once: the client binds the reply port with address/port sharing (SO_REUSEADDR + SO_REUSEPORT; Windows SO_REUSEADDR), so provider aliases and concurrent processes coexist and each keeps the replies from its own switch. Verified with two switches and two processes; one timing-sensitive test made deterministic.

## 0.24.1

- Security: transitive dependencies bumped (govulncheck clean, make vuln and CI step); fuzz tests for the packet and settings parsers; flaky test fixed; backup and drift-check helpers; decision records and runbook.

## 0.24.0

- Plan-time validation of every argument (ranges, enums, rate steps, hostname rules, cross-field storm/mirror/QoS rules); documentation examples are validated by make validate-examples and in CI; internal cleanup (named protocol layouts, per-consumer client interfaces, shared Create/Update and import helpers).

## 0.23.0

- Test coverage raised to 100% effective (2 documented main() statements excluded; make cover enforces the policy). Fixes a plugin crash when host, username or password are unknown at configure time: Configure now returns an error naming the attribute.

## 0.22.0

- Resource tplinkeasysmart_system (device description) and data source tplinkeasysmart_config_backup (size, SHA-256, optional content read over ESCP with fragment reassembly); settings table updated.

## 0.21.0

- Resource tplinkeasysmart_vlan_port_based_group (port-based VLAN groups; mode stays read-only) with the port-in-use guard.

## 0.20.0

- Resource tplinkeasysmart_lag (link aggregation groups) with a guard on old and new member ports; LAG1 imported with prevent_destroy.

## 0.19.0

- Resource tplinkeasysmart_port_mirror with the destination-port guard.

## 0.18.0

- Resource tplinkeasysmart_storm_limit (per-port storm control) with the port-in-use guard.

## 0.17.0

- Resource tplinkeasysmart_bandwidth_limit (per-port ingress/egress limits) with the port-in-use guard.

## 0.16.0

- Resource tplinkeasysmart_qos (mode and per-port priorities; priorities only accepted in port-based mode).

## 0.15.0

- Resource tplinkeasysmart_port (admin state, speed, flow control) with a guard that refuses ports that have a link up or are in a LAG unless allow_disruptive is set; client SetHostname added but not live-verified (no resource yet); live toggle tests for IGMP snooping and loop prevention.

## 0.14.0

- System info now also reports port count, auto-save and the factory-default flag; README lists all covered settings.

## 0.13.0

- Data source tplinkeasysmart_storm_control (per-port storm control; read-only).

## 0.12.0

- Data source tplinkeasysmart_bandwidth_control (per-port ingress/egress limits; read-only).

## 0.11.0

- Data source tplinkeasysmart_qos (mode and per-port priorities; read-only).

## 0.10.0

- Data source tplinkeasysmart_vlan_8021q (mode, VLAN table, per-port PVIDs; read-only).

## 0.9.0

- Data source tplinkeasysmart_vlan_port_based (read-only).

## 0.8.0

- Data source tplinkeasysmart_vlan_mtu (read-only).

## 0.7.0

- Data source tplinkeasysmart_port_mirror (read-only).

## 0.6.0

- Resource tplinkeasysmart_loop_prevention.

## 0.5.0

- Resource tplinkeasysmart_igmp_snooping (enabled, report suppression, learned groups); generic SET helper; live test harness (go test -tags live).

## 0.4.0

- Data source tplinkeasysmart_lags (link aggregation groups and member ports).

## 0.3.0

- Data source tplinkeasysmart_port_statistics (per-port traffic counters).

## 0.2.0

- Data source tplinkeasysmart_ports (port state, LAG, configured/actual speed, flow control); client is now lazy.

## 0.1.0

- Native ESCP (UDP 29808/29809) client: discovery, RSA session-key exchange, login, LED get/set, request retry, session recovery.
- Resource `tplinkeasysmart_led`; data source `tplinkeasysmart_system_info`.
- Verified on a TL-SG108E v6 (fw 1.0.0 Build 20230218): import, no-op plan, LED off/on apply cycle, restore.
