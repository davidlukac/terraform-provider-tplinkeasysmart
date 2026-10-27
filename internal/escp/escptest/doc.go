// Package escptest is an in-process TL-SG108E that speaks ESCP over loopback UDP, for
// tests of code that uses the escp client. It imports only the wire codec, never the
// client, so the escp package's own tests can use it without an import cycle.
//
// It models firmware 20230218 closely enough for the client's reads and writes (key
// exchange, credentials on every SET, fragmented configuration backup) and offers
// switches for the failures the client must survive: silence, dropped packets, rejected
// writes and stale discovery replies. It is a test double: it keeps almost no settings
// state, and it is no substitute for the real switch.
//
// A test starts one switch and aims a client at it:
//
//	sw := escptest.Start(t)
//	c, err := escp.Dial(escp.Config{
//		Host: "127.0.0.1", Username: escptest.Username, Password: escptest.Password,
//		SwitchPort: sw.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: escptest.Loopback,
//	})
//
// Faults are set inside Configure, which holds the switch's lock; what the switch
// received is read back through WriteCount, AllSets, LastSet and the other accessors, in
// the package's own Setting type.
//
// # Stability
//
// Version 0, like the escp package: names may change without notice. The switch must
// never be used outside tests.
package escptest
