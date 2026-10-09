# Changelog

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
