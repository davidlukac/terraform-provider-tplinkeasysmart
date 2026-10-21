// Package escp implements the TP-Link Easy Smart UDP management protocol as
// spoken by TL-SG108E v6 switches. See PROTOCOL.md in this
// directory for the wire format.
//
// The byte level (packet header, TLV catalogue, RC4 and RSA) is in the internal
// package wire; this package holds the client, the settings and their validation.
package escp
