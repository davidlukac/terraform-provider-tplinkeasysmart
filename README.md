# terraform-provider-tplinkeasysmart

Terraform provider for TP-Link **Easy Smart** switches, driving the switch through its native UDP management protocol (ESCP) instead of scraping the web UI. Developed against a **TL-SG108E hardware v6** (firmware 1.0.0 Build 20230218).

**Status: v0.1.0, proof of concept.** One resource and one data source. See `../docs/escp-protocol.md` for how the protocol was reverse-engineered and `../docs/terraform-plan.md` / `../docs/escp-research-plan.md` for the roadmap.

## Resources and data sources

Writable resources are limited to settings that cannot cut management access. Everything else is read-only on purpose (see `../CLAUDE.md`). Every read was checked against the web UI on switch 01.

| Name | Kind | Web UI page | Notes |
|---|---|---|---|
| `tplinkeasysmart_led` | resource | System > LED On/Off | `enabled`. Import id `led`. |
| `tplinkeasysmart_igmp_snooping` | resource | Switching > IGMP Snooping | `enabled`, `report_message_suppression`, computed `multicast_groups`. Import id `igmp_snooping`. |
| `tplinkeasysmart_loop_prevention` | resource | Monitoring > Loop Prevention | `enabled`. Import id `loop_prevention`. |
| `data.tplinkeasysmart_system_info` | data | System > System Info / IP Setting | model, hostname, MAC, firmware, hardware, IP/mask/gateway, DHCP, port count, auto-save, factory flag |
| `data.tplinkeasysmart_ports` | data | Switching > Port Setting | state, LAG, configured/actual speed, flow control |
| `data.tplinkeasysmart_port_statistics` | data | Monitoring > Port Statistics | traffic counters |
| `data.tplinkeasysmart_lags` | data | Switching > Port Trunk | LAG groups and members |
| `data.tplinkeasysmart_port_mirror` | data | Monitoring > Port Mirror | mirroring config |
| `data.tplinkeasysmart_vlan_mtu` | data | VLAN > MTU VLAN | mode and uplink port |
| `data.tplinkeasysmart_vlan_port_based` | data | VLAN > Port Based VLAN | mode and groups |
| `data.tplinkeasysmart_vlan_8021q` | data | VLAN > 802.1Q VLAN / PVID | mode, VLAN table, PVIDs |
| `data.tplinkeasysmart_qos` | data | QoS > QoS Basic | mode and port priorities |
| `data.tplinkeasysmart_bandwidth_control` | data | QoS > Bandwidth Control | ingress/egress limits |
| `data.tplinkeasysmart_storm_control` | data | QoS > Storm Control | per-port storm control |

Not covered (deliberately): user account, IP setting writes, config backup/restore, reboot, factory reset, firmware upgrade, cable diagnostics (the test can briefly drop a link).

## Provider configuration

```hcl
provider "tplinkeasysmart" {
  host            = "192.0.2.120"   # or TPLINK_EASYSMART_HOST
  username        = "admin"            # or TPLINK_EASYSMART_USERNAME
  password        = var.password       # or TPLINK_EASYSMART_PASSWORD (sensitive)
  timeout_seconds = 3                  # optional
}
```

## Constraints (read these)

- **Same layer-2 network only.** ESCP is UDP broadcast based; run Terraform on a host in the switch's subnet.
- **One switch per Terraform process.** The switch always replies to UDP port 29809, so the provider binds that port. Close the vendor Easy Smart utility first, and don't use several `tplinkeasysmart` provider aliases in one run.
- **No real security on the wire.** The protocol's encryption keys are public or trivially breakable (see the protocol doc). Keep the switches on a trusted management network.
- Only tested on TL-SG108E v6, firmware 20230218. Newer firmware (20250710) and other models are untried.

## Develop

```sh
go test ./...                      # protocol golden vectors, fake UDP switch, Terraform lifecycle tests (needs `terraform` on PATH)
go build -o bin/terraform-provider-tplinkeasysmart .
../scripts/tf.sh plan              # runs ../terraform/switch-01 against switch 01 with a temporary dev_overrides CLI config
```

Tests do not touch hardware: `internal/escp` runs the client against an in-process fake switch (it can decrypt the session key because the switch's 64-bit RSA modulus factors trivially), and `internal/provider` runs real `terraform` plan/apply/import cycles against a fake client.

## Layout

- `internal/escp`: crypto (static RC4, 8-byte-state session RC4, 64-bit RSA), packet codec, UDP client (discovery, key exchange, LED get/set, retry, session recovery).
- `internal/provider`: provider, `tplinkeasysmart_led`, `tplinkeasysmart_system_info`. Resources talk to the `SwitchClient` interface so a different transport (or the web UI) can be plugged in later.
