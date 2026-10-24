// Package safety holds the rules that decide whether a write to a switch is prudent:
// which firmware builds the client was verified against, and what a port is used for
// (link, link aggregation). It has no Terraform types and no logging; it reads from a
// switch only through the small interfaces it is given, and reports what it finds as
// typed errors and values so each front end (the provider, a command line) words the
// refusal in its own vocabulary.
//
// # Firmware
//
// The protocol is not versioned, and a newer firmware may change layouts or
// authentication. FirmwareGate returns an escp.WriteGate that refuses every write while
// the switch runs a build outside VerifiedFirmwareBuilds, and lets reads through:
//
//	c, err := escp.Dial(escp.Config{Host: host, Username: user, Password: pw, WriteGate: safety.FirmwareGate()})
//
// A front end that offers an opt-in leaves the gate out (a nil WriteGate gates nothing).
// A refusal is a *FirmwareError; branch on its Reason and word the message for your users.
//
// # What the write is
//
// A gate is also told the operation: the client method, the ports it names and its Impact
// (cosmetic, traffic or management: the worst the method can do, not a judgement of one
// particular write: a port setting is Traffic even when it disables the uplink). MaxImpact
// refuses above a class with an *ImpactError. Which ports a write touches is a separate
// question, answered by PortGate: it refuses a write that names (or, for a group it replaces
// or removes, drops) a port that is missing, linked or in a LAG, with the same *PortUseError
// as CheckPorts. DryRun records the operations a front end would perform and refuses them all
// with ErrDryRun, so a "--dry-run" is a gate and not a second code path. Chain combines
// gates; put the dry run last so it only sees what the others allowed:
//
//	dry := safety.NewDryRun()
//	gate := safety.Chain(safety.FirmwareGate(), safety.MaxImpact(escp.ImpactTraffic), safety.PortGate(), dry.Gate())
//
// A ceiling on the impact alone is not a safety rule: without PortGate (or the front end's own
// check of the ports) a write within the ceiling can still cut the uplink.
//
// # Ports
//
// UseOf turns a reading of the ports into facts about one port: does it exist, does it
// have a link, is it in a link aggregation group. A port with a link may carry the
// management path or the uplink, so writes to it need an explicit decision.
//
// CheckPorts applies the one rule every front end shares to the ports a write names: each
// must exist, have no link and belong to no LAG (an edit of a LAG tolerates the members of
// that group, through PortRule). The first offender comes back as a *PortUseError; branch on
// its Reason and word the refusal, and the way to proceed, for your users. Whether to run the
// check at all (an opt-in such as allow_disruptive) stays with the caller.
//
// # Stability
//
// Version 0, like the escp package it belongs to: names may change without notice until
// the library is released on its own. VerifiedFirmwareBuilds grows only after a build has
// been tested on a real switch.
package safety
