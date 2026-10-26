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
// # Naming
//
// A field that holds a port number is named for what the record is. A record that is a
// port (Port, PortConfig, PortStats) calls it ID; a record that refers to a port (PortBandwidth,
// PortStorm, StormConfig, PortPVID, PortPriority) calls it Port; a port with a role in
// the record is named for the role (MTUVLAN.UplinkPort, Mirror.MirrorPort). Readers are
// named for what they return (Ports, QoS, LED, Dot1Q), writers Set... and Delete....
//
// # Layout of the package
//
// A setting is one file: its types, its reader, its writer, its parser and its validation live
// together, and its tests sit in the file of the same name with the suffix _test (ports.go,
// ports_test.go). The settings are leaves: they depend on the client and on the shared
// helpers, never on each other (except Port, the type most of them refer to).
//
//   - Settings: ports, lag, qos, bandwidth, storm, igmp, mirror, loopprevention, led, system,
//     stats, backup, vlan_mtu, vlan_port, vlan_mode, vlan_8021q (the reads) and
//     vlan_8021q_write. system.go is the device description, ip.go the management address
//     (SetIPConfig and its lost-reply handling).
//   - The client: client.go (Client, Dial, Close, the socket), config.go (Config, WriteGate,
//     InfoReader, Info), info.go (reading Info), session.go (the key exchange and its retry),
//     transport.go (packets, encryption, sending, receiving, fragments, deadlines), request.go
//     (the one place that sends a gated SET and a GET, and the port count) and errors.go.
//   - Shared: operation.go (the Operation a gate sees and the Impact classes), limits.go (the
//     public limits and Validate functions), bitmask.go (port masks) and discover.go (finding
//     switches without credentials).
//
// Sub-packages hold what has a boundary of its own: internal/wire (the packet codec, the
// session crypto and every TLV type number, which no caller needs), internal/udp (sharing the
// reply port between sockets), safety (the policy gates and the compatibility table) and
// escptest (a fake switch for tests). Tests are named for the source they exercise; the ones
// that cut across sources have a name of their own: cancel_test (cancellation), parsers_test,
// readers_test, write_gate_test, dryrun_test, fake_switch_test (the fake itself) and
// helpers_test. CONTRIBUTING.md in the repository root is the checklist for adding or
// changing a setting.
//
// # Using a client
//
// Dial prepares a Client for one switch; nothing is sent to it until the first call.
// Reads (Ports, Dot1Q, QoS and the rest; named for what they return) cannot change the
// switch. Writes are the methods named Set... and Delete...:
//
//	c, err := escp.Dial(escp.Config{Host: "192.0.2.11", Username: "admin", Password: pw})
//	if err != nil { ... }
//	defer c.Close()
//	ports, err := c.Ports(ctx)
//
// A Client is safe for concurrent use, but it sends one request at a time because the
// switch answers one request at a time. Use one Client per switch and share it between
// goroutines. Clients for different switches work side by side, in one process or on one
// host: switches always reply to UDP port 29809, the clients share that port, and each
// keeps only the replies from its own switch. Two Clients for the same switch do not work
// at all (measured: 30 reads out of 30 failed, and the switch dropped pings meanwhile).
// The switch appears to keep a single session, so that each key exchange invalidates the
// other client's. Discover can run next to a Client. Port sharing was observed on macOS with
// real switches, and a test shows that on Linux a broadcast reaches every sharing socket; where
// the system offers no sharing option the second bind fails with an error.
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
//     before any write is sent. A writer that names a port also reads the switch's port count
//     once (the first time), to refuse ports the switch does not have: that is a read, and it
//     happens before the gate.
//  2. The Config.WriteGate, if there is one, is consulted with the write's Operation (the
//     method, the ports it names and its Impact class). When it refuses, the method returns
//     the gate's error unchanged and sends no write. The safety package provides gates
//     for firmware builds this client was not verified against, for a ceiling on the impact,
//     for the ports a write touches, and for a dry run, and Chain combines them.
//  3. The write is sent with the credentials, which ride on every SET. If the switch
//     answers with a non-zero code the error is a *RejectedError (it matches ErrRejected).
//
// The client does not know which ports carry traffic, and apart from SetIPConfig (which
// waits for the switch to answer at its new address) no write reads back to check that it
// took effect. Deciding whether a write is prudent (is the port in use? is it in a link
// aggregation group?) and verifying the result are the caller's job. The doc comment of
// each write says what it can disturb, and the safety package answers the common question
// of what a port is used for. A few writes read the current settings first so they can
// send the rest unchanged (SetHostname, SetPortPriorities); their comments say so. Every write
// that names a port also refuses ports the switch does not have (ErrInvalid): the client reads
// the switch's port count once, on the first such write, and keeps it.
//
// # Errors
//
// Test errors with errors.Is and errors.As, never by message: ErrNoReply (the switch did
// not answer in time), ErrRejected and *RejectedError (the switch refused a write),
// ErrNoInterface (Discover has nowhere to send), ErrMoveUnconfirmed (SetIPConfig lost
// track of the switch), ErrInvalidConfig (a configuration backup that is not one) and
// ErrInvalid (an argument refused before any write was sent; the message names the value and
// what is allowed), ErrReplyTooLarge (a fragmented reply bigger than any switch sends).
//
// # Security
//
// ESCP has no real secrecy. The password travels in every write, protected only by RC4
// with a key exchanged under a 64-bit RSA key, which anyone who can capture the traffic
// can break. Use it on a network you trust, and never log a Config.
package escp
