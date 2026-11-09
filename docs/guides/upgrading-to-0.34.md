---
page_title: "Upgrading to 0.34.0"
subcategory: ""
description: |-
  What changes for your configuration and your state when you move from 0.33.x to 0.34.0, and how to migrate.
---

# Upgrading to 0.34.0

Version 0.34.0 makes the schema and the behavior of a few settings consistent **before** the interface is frozen at 1.0, so it contains breaking changes. It does not say how close 1.0 is: more minor versions may follow, each with its own migration notes. Every one of them has a migration step below, none needs a state rewrite, and the order that is safe is: read this page, bump the version, run `terraform plan`, fix what the plan or the validation reports, then apply.

It also pulls in version 0.9.0 of the ESCP library, which changes how clients behave when the switch is silent, closed or shared (see "Behavior you may notice").

~> **Before you upgrade** take a configuration backup (`tplinkeasysmart_config_backup`, or `scripts/backup-config.sh` if you use the maintainer's workspace) and run `terraform plan` with the **old** version so you know the plan is clean. After the upgrade the plan must show **no changes to resources** except what is listed under "Plan differences you should expect"; anything else is a bug worth reporting.

## Quick checklist

1. `terraform plan` on 0.33.x is clean.
2. Pin `version = "~> 0.34"` and `terraform init -upgrade`.
3. Run `terraform validate`; fix the attribute names it reports (data sources: tables below).
4. Using a `switches` map? Add `switch = "<name>"` to **every** resource and data source (`switch = "default"` for the provider's own switch). See "Several switches".
5. Using `host` **and** `mac` and relying on the MAC to find the switch when its address changes? Set `relocate_by_mac = true`.
6. Changing `loop_prevention`? Add `allow_disruptive = true` for that apply.
7. `terraform plan`: expect only the differences listed below, then `terraform apply`.

## Data sources

| Before (0.33.x) | Now (0.34.0) | Migration |
|---|---|---|
| `data.tplinkeasysmart_qos` `mode` was a number (0, 1, 2) and `mode_name` the name | `mode` is the name (`port-based`, `802.1p`, `dscp-802.1p`) and `mode_name` is gone | Replace `…mode_name` with `…mode`; compare with the strings, not the numbers |
| `tplinkeasysmart_ports` `ports[].id` is the port number | `ports[].port` | `p.id` becomes `p.port` in `for` expressions and outputs |
| `tplinkeasysmart_port_statistics` `ports[].id` and `ports[].link` | `ports[].port` and `ports[].actual_speed` | `p.id` becomes `p.port`, `p.link` becomes `p.actual_speed` |
| `tplinkeasysmart_vlan_mtu` `uplink_port` was `1` when the MTU VLAN is off | `uplink_port` is `null` when it is off | Compare `enabled` first, or with `null` |
| `tplinkeasysmart_port_mirror` `mirror_port` was `0` when mirroring is off | `mirror_port` is `null` when it is off | As above |
| `multicast_groups` was an attribute of the `tplinkeasysmart_igmp_snooping` **resource** | Removed there; read it from the new data source `tplinkeasysmart_igmp_snooping` (`enabled`, `report_message_suppression`, `multicast_groups`) | Replace `tplinkeasysmart_igmp_snooping.x.multicast_groups` with `data.tplinkeasysmart_igmp_snooping.x.multicast_groups` |

The `id` of a LAG or VLAN **element** (`lags[].id`, `vlans[].id`) is unchanged: it is the id of the group or the VLAN, not a port.

## Resources

- **`tplinkeasysmart_port_mirror`**: `mirror_port` is optional without a default and must be 1 or higher. Remove `mirror_port = 0` from a configuration that disables mirroring (leave the attribute out). Enabled mirrors are unchanged.
- **`tplinkeasysmart_lag` and `tplinkeasysmart_vlan_8021q_group`** refuse to *create* an object that already exists on the switch (VLAN 1 always does), like `tplinkeasysmart_vlan_port_based_group` always did, because the create would overwrite it silently. If you hit "already exists": `terraform import` it (the error prints the command) and apply again. Objects already in your state are not affected.
- **`tplinkeasysmart_loop_prevention`** has `allow_disruptive`. Changing the value (either way) is refused without `allow_disruptive = true`, and applying the value the switch already has writes nothing (it used to write). Add the flag for the apply that changes the value; remove it afterwards.
- **`tplinkeasysmart_vlan_mode`**: an MTU VLAN is refused, even with `allow_disruptive`, unless `mtu_uplink_port` is a port that has a link up and is in no LAG. The flag is the consent to the mode change, not to isolating the switch from the host that manages it.
- **`tplinkeasysmart_config_backup`** (data source) now passes the firmware gate: on a firmware build the provider is not verified against it is refused unless `allow_unverified_firmware = true` (the file is read with a request that is a write on the wire).

## Several switches

A configuration with a `switches` map must now say which switch every resource and data source is for. Before, an object without `switch` went to the provider's own switch, or to the only named switch, so adding a second entry to the map changed the meaning of every such object.

1. Add `switch = "<name>"` to every resource and data source.
2. A provider block that has `host` (or the environment variables) **and** `switches`: select its own switch with `switch = "default"`. `default` cannot be a key of the map.
3. Ids and import ids did **not** change and are now a stable part of the interface: the own switch keeps plain ids (`led`), a named switch prefixes its name (`sw02:led`). Import with the prefix, `default:` for the own switch (`terraform import tplinkeasysmart_port.p default:7`).
4. Renaming a key of `switches` replaces everything on that switch (the objects are deleted and created; a LAG or VLAN group really is removed from the device). Rename it without touching the switch with `moved` blocks, or `terraform state mv`, before you rename the key.

The plan-time message lists the names to choose from.

## `relocate_by_mac` (new) and the MAC

With both `host` and `mac`, the client used to look the switch up by its MAC when nothing answered at `host`, and follow it to whatever address answered. That surprised people (a typo in `host` was papered over; a device at the address was followed away). It is now **off by default**: the client keeps to `host`, and a switch that is not there is an error.

If your switch has a DHCP lease that changes and you named it with both, set `relocate_by_mac = true` at the provider, per entry of `switches`, or with `TPLINK_EASYSMART_RELOCATE_BY_MAC=true`. It needs `mac`. A switch named by `mac` alone, with no `host`, is still found by its MAC. The `switch = "default"` import prefix and `switch` value only matter when the provider has a `switches` map.

## Behavior you may notice

- **A switch that does not answer is asked once.** After the first request of a run that gets no answer (while nothing has answered), every later request to that switch fails at once with the same error instead of costing the full timeout again.
- **A write that got no reply says so.** The error now says the write *may have been applied*; read the setting back before you apply again. Management-class writes (addresses, VLANs, LAGs, mirror, port settings) first prove the session with one extra read and are never sent twice.
- **One client per switch.** Two clients of one switch in one process are refused at the second one's first call, whether they name the switch by address or by MAC. The provider already built one per switch; this only matters for tools that use the library next to it.
- **A destroy plan warns** when an `allow_disruptive = true` kept in state would let a removed LAG, VLAN group or PVID act on a port that has a link up (`lifecycle { prevent_destroy = true }` blocks it).
- **`Close` is final** in the library. Programs that embed the library (not the provider) see `ErrClosed` after `Close`; see the library's changelog.

## Plan differences you should expect

- Outputs that print a data source whole show the renamed attributes (`port`, `actual_speed`, `mode` as a string, `null` ports). Nothing is written.
- Resources that were imported or created with 0.33.x show **no** differences. `loop_prevention` gains `allow_disruptive = false` in state on the first refresh; `multicast_groups` disappears from the state of `igmp_snooping`; `mirror_port` of a disabled mirror becomes `null`.

## If something goes wrong

Nothing in this release changes a stored value on the switch by itself. If a plan shows a change you did not expect, do not apply: run `terraform plan -refresh-only` and compare with a fresh `config_backup` hash. To go back, pin `version = "0.33.0"` and `terraform init -upgrade`; if Terraform then objects to the state, restore the `terraform.tfstate.backup` that it keeps next to the state (or your remote state's previous version) and run `terraform plan` again.
