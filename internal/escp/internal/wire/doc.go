// Package wire is the byte level of the ESCP protocol: the 32-byte packet header, the
// type-length-value elements, the catalogue of TLV types, and the RC4 and RSA
// primitives that protect them. It knows nothing about settings, sockets or
// switches.
//
// The package is internal to the escp package: the settings code in the parent package
// and the fake switch in escptest use it, nobody else, and nothing here is a stable
// interface. The meaning of the numbers is documented in ../../PROTOCOL.md.
//
// Packets are untrusted input. Unmarshal bounds-checks every length it reads and returns
// an error for a truncated or inconsistent packet; it never panics.
package wire
