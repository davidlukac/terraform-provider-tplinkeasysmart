# Changelog

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
