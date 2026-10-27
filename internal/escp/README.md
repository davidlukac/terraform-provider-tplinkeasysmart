# escp: a Go client for TP-Link Easy Smart switches

`escp` talks the switches' native UDP management protocol (ESCP), as spoken by the vendor's configuration utility. It reads and writes the settings of a TL-SG108E v6, finds switches on the network, validates every value before anything is sent, and ships the safety gates and a fake switch that a front end needs to use it responsibly. It is the library behind the [Terraform provider](../../README.md) in this repository, and is written to be reused by a command line client and by other programs.

**Status: version 0.** The package lives under `internal/` and may change without notice until it is extracted into a module of its own and released as 1.0 (see the stability section of the package documentation). Which hardware and firmware it was verified on is in the [compatibility table](../../README.md#compatibility); the wire format is in [PROTOCOL.md](PROTOCOL.md).

> **The switch is real network equipment with no console.** A wrong write can cut the management path or the uplink, and recovery is a physical factory reset. Read [Writing safely](#writing-safely) before the first write.

## Packages

| Package | What it is | Public |
|---|---|---|
| `escp` (this directory) | the client, the domain types, validators, `Discover` | yes |
| `escp/safety` | write gates (firmware, ports, impact, dry run) and the compatibility table | yes |
| `escp/escptest` | a fake switch on a loopback UDP port, for tests | yes |
| `escp/internal/wire` | packet codec, session crypto, the TLV catalogue | no |
| `escp/internal/udp` | sharing the reply port between sockets | no |

The file layout inside `escp` (one file per setting) is described in the package overview, `go doc .`.

## Quick start

```go
c, err := escp.Dial(escp.Config{Host: "192.0.2.11", Username: "admin", Password: escp.Password(pw)})
if err != nil { /* ... */ }
defer c.Close()

info, err := c.Info(ctx)     // model, firmware, MAC, address: no credentials needed
ports, err := c.Ports(ctx)   // reads cannot change the switch
for _, p := range ports {
	fmt.Printf("port %d: %s (configured %s)\n", p.ID, p.ActualSpeed, p.ConfiguredSpeed)
}
```

- `Dial` sends nothing and binds no socket; the first call does the key exchange.
- A `Client` is safe for concurrent use but sends one request at a time, because the switch answers one at a time. **Use one `Client` per switch.** Clients for different switches work side by side; two for the same switch fail (the switch keeps one session).
- Every method takes a `context.Context`. Cancelling it stops the call, and nothing is written once it is done.
- `Discover(ctx, DiscoverOptions{})` finds the switches on the local network without credentials or addresses.

## Finding a switch by MAC address

A switch's address can change; its MAC address does not. Put it in the configuration and the client pins the identity and finds the switch when it has to:

```go
mac, _ := escp.ParseMAC("c0:3a:55:6c:81:61") // escp.MAC: a comparable value, the zero value means "no MAC"
c, err := escp.Dial(escp.Config{MAC: mac, Username: user, Password: pw}) // no Host: found by discovery on the first request
```

- With `Config.MAC` set, a device that answers with another MAC is refused (`ErrWrongSwitch`, naming both) and nothing is written to it. The MAC is checked on every reply, the key exchange's included.
- With no `Host`, or a `Host` that stops answering, the client looks the switch up by its MAC (a discovery probe that stops at the first reply from that switch, normally under a second; `ErrNotFound` when nobody answers, after a second try of the lookup, because one right after a management move once missed a switch that was there). `escp.Locate(ctx, mac, opts)` is the same lookup for a front end.
- `Dial` still sends nothing; the lookup happens on the first request.

## Writing safely

Every write follows the same steps: the arguments are validated and a bad value is refused before anything is sent (`errors.Is(err, escp.ErrInvalid)`); the optional **write gate** is asked; the write is sent with the credentials; a refusal by the switch is a `*RejectedError`.

```go
c, err := escp.Dial(escp.Config{
	Host: host, Username: user, Password: pw,
	WriteGate: safety.Chain(
		safety.FirmwareGate(),               // only firmware builds this client was verified against
		safety.MaxImpact(escp.ImpactTraffic), // nothing that can cut the management path
		safety.PortGate(),                    // only ports with no link and no LAG
	),
})
err = c.SetPort(ctx, escp.PortConfig{ID: 3, Enabled: true, Speed: escp.SpeedAuto})
var refused *safety.PortUseError
if errors.As(err, &refused) { /* the gate refused; nothing was sent */ }
```

- A gate sees the `Operation` (the method, the ports it names, its `Impact`) and returns an error to refuse. Gates are plain functions: write your own, or combine with `safety.Chain`.
- **Impact** is the worst the method can do with any arguments: `ImpactCosmetic` (LED, device description), `ImpactTraffic` (port settings, rate limits, storm control, QoS, IGMP, mirroring, VLAN groups) or `ImpactManagement` (the IP settings, the VLAN mode, PVIDs, link aggregation, loop prevention).
- `safety.NewDryRun()` records the operations a command would perform and sends none of them; it still needs a reachable switch, because some writers read first.
- The client does not know which ports carry traffic and, apart from `SetIPConfig`, does not read back after a write. Deciding whether a write is prudent (`safety.UseOf` says what a port is used for) and verifying the result are the caller's job.
- **Restoring is explicit.** There is no "reset": to put a setting back, write the value you recorded.

## What it can read and write

| Setting (web UI page) | Read | Write | Types | Impact of the write |
|---|---|---|---|---|
| Device (System Info) | `Info`, `System` | `SetHostname`, `SetIPConfig` | `Info`, `System`, `IPConfig` | cosmetic, management |
| LED | `LED` | `SetLED` | `bool` | cosmetic |
| Ports (Port Setting) | `Ports` | `SetPort` | `Port`, `PortConfig`, `Speed` | traffic |
| Port statistics | `PortStatistics` | (read only) | `PortStats` | |
| Link aggregation (Port Trunk) | `LAGs` | `SetLAG` | `LAG` | management |
| Port mirror | `PortMirror` | `SetPortMirror` | `Mirror` | traffic |
| Bandwidth control | `Bandwidth` | `SetBandwidth` | `PortBandwidth` | traffic |
| Storm control | `StormControl` | `SetStormControl` | `PortStorm`, `StormConfig` | traffic |
| QoS Basic | `QoS` | `SetQoSMode`, `SetPortPriorities` | `QoS`, `QoSMode`, `Priority` | traffic |
| IGMP snooping | `IGMP` | `SetIGMP` | `IGMP`, `IGMPConfig`, `IGMPGroup` | traffic |
| Loop prevention | `LoopPrevention` | `SetLoopPrevention` | `bool` | management |
| VLAN mode | `VLANMode` | `SetVLANMode` | `VLANMode`, `VLANModeSetting` | management |
| MTU VLAN | `MTUVLAN` | (through `SetVLANMode`) | `MTUVLAN` | |
| Port-based VLAN | `PortBasedVLAN` | `SetPortBasedVLAN` | `PortBasedVLAN`, `PortVLAN` | traffic |
| 802.1Q VLAN and PVID | `Dot1Q` | `SetVLAN8021Q`, `DeleteVLAN8021Q`, `SetPVIDs` | `Dot1Q`, `VLAN8021Q`, `PortPVID` | traffic, management |
| Configuration file | `ConfigBackup` | (restore is not offered) | `ConfigFile` | |

Deliberately **not** offered: user account, config restore, reboot, factory reset and firmware upgrade.

## Data structures

- **Ports are numbers from 1**: a `PortID` for one, a `PortSet` for a set (a comparable value held as the wire bitmap: no duplicates, ascending, `Union`/`Minus`/`Intersect`, `escp.NewPortSet(2, 3)` refuses a port outside 1 to `MaxPort`). A record that *is* a port has `ID` (`Port`, `PortConfig`, `PortStats`); a record that *refers to* a port has `Port` (`PortBandwidth`, `PortStorm`, `PortPVID`, `PortPriority`); a port with a role is named for it (`MTUVLAN.UplinkPort`, `Mirror.MirrorPort`).
- **Closed sets are types** with `String`, `Parse...`, `Valid` and a list function: `Speed` (`ConfigurableSpeeds`), `QoSMode` (`QoSModes`), `Priority` (1 to 4, as the web UI shows it), `VLANMode` (`VLANModes`). The spellings are the web UI's and the Terraform provider's, and are part of its state.
- **Readers return values, not pointers.** Counters in `PortStats` are the switch's 32-bit counters and wrap.
- **Limits and validators are public** so a front end can refuse a bad value before it reaches the client: `PortID.Validate`, `VLANID.Validate`, `Kbps.Validate`, `VLANName.Validate`, `Hostname.Validate`, `ValidateLAG`, `ValidateIPConfig`, and the constants they use (`MaxPort`, `RateStepKbps`, `MaxBandwidthKbps`, `LAGGroupCount`, ...).

## Errors

Test with `errors.Is` and `errors.As`, never by message.

| Error | Meaning |
|---|---|
| `ErrInvalid` | an argument was refused before any write was sent; the message names the value and what is allowed |
| `ErrNoReply` | the switch did not answer in time |
| `ErrRejected`, `*RejectedError` | the switch refused a write; the error carries the code (and, for the known ones, the reason) |
| `ErrMoveUnconfirmed` | `SetIPConfig` lost track of the switch: look at both addresses before believing either |
| `ErrNoInterface` | `Discover` has nowhere to send |
| `ErrInvalidConfig` | a configuration backup that is not one |
| `ErrReplyTooLarge` | a fragmented reply bigger than any switch sends |
| `ErrWrongSwitch`, `*WrongSwitchError` | the device that answered is not the switch `Config.MAC` names; nothing was written to it |
| `ErrNotFound` | no switch with that MAC answered a discovery probe |
| `*safety.FirmwareError`, `*safety.PortUseError`, `*safety.ImpactError` | a gate refused; nothing was sent |

## Testing against a fake switch

`escptest` starts a UDP server on a loopback port that speaks the protocol (RSA key exchange, RC4, TLVs, fragments) and can misbehave on request (dropped packets, a mute switch, a switch that leaves after an address change, a broken backup). It is the same fake the library's own tests use.

```go
func TestMyCode(t *testing.T) {
	f := escptest.Start(t)
	f.EnableModel(map[int]escptest.LinkSpeed{1: escptest.Link1000Full}) // a stateful switch that enforces the measured rules
	c, _ := escp.Dial(escp.Config{Host: "127.0.0.1", SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t),
		BroadcastAddr: escptest.Loopback, Username: escptest.Username, Password: escptest.Password})
	// ... use c, then ask f what it received: f.WriteCount(), f.AllSets()
}
```

`SwitchPort`, `HostPort` and `BroadcastAddr` exist only for this; a real switch always uses 29808 and 29809.

## Security

ESCP has no real secrecy: the password travels in every write, protected only by RC4 with a key exchanged under a 64-bit RSA key that anyone who can capture the traffic can break. Use it on a network you trust, never log a `Config`, and do not reuse the switch password elsewhere.

## See also

[PROTOCOL.md](PROTOCOL.md) (wire format and measured behaviour) · [CONTRIBUTING.md](../../CONTRIBUTING.md) (how to add or change a setting) · `go doc -all .` and `pkgsite` for the full reference.
