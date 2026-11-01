# Changelog

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
