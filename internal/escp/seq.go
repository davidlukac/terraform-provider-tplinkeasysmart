package escp

import "github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"

// Sequence numbers pair a reply with its request, and every reply goes to the one UDP port 29809
// whoever asked (see PROTOCOL.md), so the clients and the discovery of one process hear each
// other's replies. They therefore draw from disjoint ranges: a late reply to a discovery probe,
// for instance one that arrives after the first request of a client that has just found its
// switch, can never carry the sequence number the client is waiting for. (Before the ranges were
// split, both drew from 0-999 and the two collided now and then, which showed up once as an
// unexpected reply in a regression run.)
const (
	// clientSeqSpace is the number of sequence numbers a client counts through, 0 to 999.
	clientSeqSpace = 1000
	// discoverySeqBase and discoverySeqSpace are the range discovery probes draw from, 1000 to 1999.
	discoverySeqBase  = clientSeqSpace
	discoverySeqSpace = 1000
)

// startSeq is a client's first sequence number, from two random bytes.
func startSeq(n [2]byte) uint16 { return (uint16(n[0])<<8 | uint16(n[1])) % clientSeqSpace }

// nextSeq is the sequence number after seq: a client counts up and wraps within its range.
func nextSeq(seq uint16) uint16 { return (seq + 1) % clientSeqSpace }

// discoverySeq is the sequence number of one discovery, from two random bytes.
func discoverySeq(n [2]byte) uint16 {
	return discoverySeqBase + (uint16(n[0])<<8|uint16(n[1]))%discoverySeqSpace
}

// requestSeq is the sequence number of the request after the one that used last. The counter
// advances whichever range the last request used, so a retry never repeats a number; a discovery
// sent through a client (Info, the lookups) takes its number from the discovery range, so that a
// late reply to it, which can sit in another client's socket, is never taken for the reply to an
// ordinary request.
func requestSeq(last uint16, op byte) uint16 {
	n := nextSeq(last % clientSeqSpace)
	if op == wire.OpDiscovery {
		return discoverySeqBase + n
	}
	return n
}
