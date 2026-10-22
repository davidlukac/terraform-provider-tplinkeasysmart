// Package escp is a client for the TP-Link Easy Smart UDP management protocol (ESCP), as
// spoken by TL-SG108E v6 switches. It reads and writes the switch's settings, finds
// switches on the network and validates every value before anything is sent. The wire
// format is described in PROTOCOL.md in this directory.
//
// # Stability
//
// This package is version 0. It lives under internal/ in the Terraform provider today and
// is written to be extracted into a module of its own; until that happens, and until it
// has a 1.0 release, exported names, types and error values may change without notice.
// Where something outside the code depends on a spelling (the names of Speed and QoSMode
// values are part of the provider's Terraform state), the doc comment of that name says so.
//
// # Using a client
//
// Dial prepares a Client for one switch; nothing is sent to it until the first call.
// Reads (Ports, VLAN8021Q, QoS and the rest) cannot change the switch. Writes are the
// methods named Set... and Delete...:
//
//	c, err := escp.Dial(escp.Config{Host: "192.168.77.11", Username: "admin", Password: pw})
//	if err != nil { ... }
//	defer c.Close()
//	ports, err := c.Ports(ctx)
//
// A Client is safe for concurrent use, but it sends one request at a time because the
// switch answers one request at a time. Switches always reply to UDP port 29809, so only
// one Client (or Discover, or the vendor's utility) can work on a host at once.
//
// Discover finds the switches on the network without credentials and without knowing
// their addresses.
//
// # Writing safely
//
// The switch is real network equipment with no console: a wrong write can cut the
// management path or the uplink, and recovery is a physical factory reset. Every write
// therefore follows the same steps:
//
//  1. The arguments are validated (see the Validate functions) and a bad value is refused
//     before anything is sent.
//  2. The Config.WriteGate, if there is one, is consulted. When it refuses, the method
//     returns the gate's error unchanged and sends nothing at all. The safety package
//     provides a gate for firmware builds this client was not verified against.
//  3. The write is sent with the credentials, which ride on every SET. If the switch
//     answers with a non-zero code the error is a *RejectedError (it matches ErrRejected).
//
// The client does not know which ports carry traffic, and apart from SetIPConfig (which
// waits for the switch to answer at its new address) no write reads back to check that it
// took effect. Deciding whether a write is prudent (is the port in use? is it in a link
// aggregation group?) and verifying the result are the caller's job. The doc comment of
// each write says what it can disturb, and the safety package answers the common question
// of what a port is used for. A few writes read the current settings first so they can
// send the rest unchanged (SetHostname, SetPortPriorities); their comments say so.
//
// # Errors
//
// Test errors with errors.Is and errors.As, never by message: ErrNoReply (the switch did
// not answer in time), ErrRejected and *RejectedError (the switch refused a write),
// ErrNoInterface (Discover has nowhere to send), ErrMoveUnconfirmed (SetIPConfig lost
// track of the switch) and ErrInvalidConfig (a configuration backup that is not one).
// Validation errors are plain messages that name the value and what is allowed.
//
// # Security
//
// ESCP has no real secrecy. The password travels in every write, protected only by RC4
// with a key exchanged under a 64-bit RSA key, which anyone who can capture the traffic
// can break. Use it on a network you trust, and never log a Config.
package escp
