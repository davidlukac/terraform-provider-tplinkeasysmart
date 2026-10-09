# Changelog

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
