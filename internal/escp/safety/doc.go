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
// # Ports
//
// UseOf turns a reading of the ports into facts about one port: does it exist, does it
// have a link, is it in a link aggregation group. A port with a link may carry the
// management path or the uplink, so writes to it need an explicit decision. The package
// states facts and leaves the verdict to the caller, because the rules differ per write
// (editing a link aggregation group tolerates the ports of that group, a VLAN edit does
// not).
//
// # Stability
//
// Version 0, like the escp package it belongs to: names may change without notice until
// the library is released on its own. VerifiedFirmwareBuilds grows only after a build has
// been tested on a real switch.
package safety
