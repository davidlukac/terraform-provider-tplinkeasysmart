# terraform-provider-tplinkeasysmart

Terraform provider for TP-Link **Easy Smart** switches, driving the switch through its native UDP management protocol (ESCP) instead of scraping the web UI. Developed against a **TL-SG108E hardware v6** (firmware 1.0.0 Build 20230218).

**Status: v0.1.0, proof of concept.** One resource and one data source. See `../docs/escp-protocol.md` for how the protocol was reverse-engineered and `../docs/terraform-plan.md` / `../docs/escp-research-plan.md` for the roadmap.

## Resources and data sources

Write support exists only where it cannot cut management access, and per-port or per-group writes are **guarded**: the provider refuses to touch a port that has a link up or belongs to a LAG unless `allow_disruptive = true` is set (see `../CLAUDE.md`). Every write path was verified on switch 01 using only unused ports (6, 7) and always restored. Nothing above the unused ports (router LAG on 1+2, switch 02 and the workstation on 4/8) was written.

| Name | Kind | Web UI page | Guard |
|---|---|---|---|
| `tplinkeasysmart_led` | resource | System > LED On/Off | none needed |
| `tplinkeasysmart_igmp_snooping` | resource | Switching > IGMP Snooping | none; real toggle verified |
| `tplinkeasysmart_loop_prevention` | resource | Monitoring > Loop Prevention | none; real toggle verified |
| `tplinkeasysmart_qos` | resource | QoS > QoS Basic | none; priorities only in port-based mode |
| `tplinkeasysmart_port` | resource | Switching > Port Setting | port in use / LAG member |
| `tplinkeasysmart_bandwidth_limit` | resource | QoS > Bandwidth Control | port in use / LAG member |
| `tplinkeasysmart_storm_limit` | resource | QoS > Storm Control | port in use / LAG member |
| `tplinkeasysmart_port_mirror` | resource | Monitoring > Port Mirror | destination port in use / LAG member |
| `tplinkeasysmart_lag` | resource | Switching > Port Trunk | old and new member ports |
| `tplinkeasysmart_vlan_port_based_group` | resource | VLAN > Port Based VLAN | old and new member ports (membership is exclusive) |
| `data.tplinkeasysmart_system_info` | data | System Info / IP Setting | read-only |
| `data.tplinkeasysmart_ports` | data | Switching > Port Setting | read-only |
| `data.tplinkeasysmart_port_statistics` | data | Monitoring > Port Statistics | read-only |
| `data.tplinkeasysmart_lags` | data | Switching > Port Trunk | read-only |
| `data.tplinkeasysmart_port_mirror` | data | Monitoring > Port Mirror | read-only |
| `data.tplinkeasysmart_vlan_mtu` | data | VLAN > MTU VLAN | read-only (mode is never written) |
| `data.tplinkeasysmart_vlan_port_based` | data | VLAN > Port Based VLAN | read-only |
| `data.tplinkeasysmart_vlan_8021q` | data | VLAN > 802.1Q VLAN / PVID | read-only |
| `data.tplinkeasysmart_qos` | data | QoS > QoS Basic | read-only |
| `data.tplinkeasysmart_bandwidth_control` | data | QoS > Bandwidth Control | read-only |
| `data.tplinkeasysmart_storm_control` | data | QoS > Storm Control | read-only |

Deliberately **not** writable: management IP / DHCP, VLAN mode switches (MTU, port-based, 802.1Q), 802.1Q VLANs and PVIDs, user account, config backup/restore, reboot, factory reset, firmware upgrade, cable diagnostics. The device description (hostname) write exists in the client (`SetHostname`) but is not live-verified and has no resource: it re-sends the management IP configuration and its live test was blocked by the permission classifier.

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

## Publishing

Release plumbing is in place (`.goreleaser.yml`, `.github/workflows/`, `terraform-registry-manifest.json`, generated `docs/` and `examples/`, `.golangci.yml`, `Makefile`), but nothing is published. Remaining manual steps, in order:

1. **Choose a license** and add `LICENSE` (the Registry needs an OSI-approved one; MPL-2.0 or MIT are typical).
2. **Decide the namespace and extract the repo**: the provider must be the root of a public GitHub repository named `terraform-provider-tplinkeasysmart`. Extract it from `local-infra` with history (for example `git subtree split --prefix=terraform-provider-tplinkeasysmart`), then change the Go module path (`go.mod` and all imports), the address in `main.go`, and the `source` in `examples/` and `docs/` (re-run `make generate`). `.github/workflows/` only works once the provider is the repo root.
3. **Create a GPG signing key**, add `GPG_PRIVATE_KEY` and `PASSPHRASE` as repository secrets, and add the public key in the Registry (Publish > Provider).
4. **Register the provider** on registry.terraform.io (sign in with GitHub, Publish > Provider, pick the repository), then push a `vX.Y.Z` tag; the release workflow builds, signs and uploads the assets and the Registry ingests them.
5. Optional: submit to the OpenTofu registry with a PR to `opentofu/registry`.
6. Before the first public release re-verify the Registry requirements against the current publishing docs, run `make snapshot` and `make lint`, and decide whether to stay `0.x` until the schema is stable.

Common tasks: `make generate` (docs), `make lint`, `make test`, `make testacc`, `make docs-check`, `make snapshot`.

## Layout

- `internal/escp`: crypto (static RC4, 8-byte-state session RC4, 64-bit RSA), packet codec, UDP client (discovery, key exchange, LED get/set, retry, session recovery).
- `internal/provider`: provider, `tplinkeasysmart_led`, `tplinkeasysmart_system_info`. Resources talk to the `SwitchClient` interface so a different transport (or the web UI) can be plugged in later.

## License

MIT, see [LICENSE](LICENSE).
