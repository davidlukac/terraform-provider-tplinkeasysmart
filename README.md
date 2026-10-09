# terraform-provider-tplinkeasysmart

Terraform provider for TP-Link **Easy Smart** switches, driving the switch through its native UDP management protocol (ESCP) instead of scraping the web UI. Developed against a **TL-SG108E hardware v6** (firmware 1.0.0 Build 20230218).

**Status: v0.1.0, proof of concept.** One resource and one data source. See `../docs/escp-protocol.md` for how the protocol was reverse-engineered and `../docs/terraform-plan.md` / `../docs/escp-research-plan.md` for the roadmap.

## Resources and data sources

| Name | What it does |
|---|---|
| `tplinkeasysmart_led` | The port LEDs (System > LED On/Off). `enabled` (bool). Import id: `led`. Destroy only forgets it. |
| `data.tplinkeasysmart_system_info` | Model, hostname, MAC, firmware, hardware, IP settings via unauthenticated discovery. Doubles as connectivity check. |

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
