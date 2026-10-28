package escp

import "testing"

// Every random input maps into its own range, and the two ranges share no number: a reply to a
// discovery probe can never be taken for the reply to a client's request, and the other way round.
func TestSequenceRangesAreDisjoint(t *testing.T) {
	client := map[uint16]bool{}
	discovery := map[uint16]bool{}
	for v := 0; v <= 0xffff; v++ {
		n := [2]byte{byte(v >> 8), byte(v)}
		client[startSeq(n)] = true
		discovery[discoverySeq(n)] = true
	}
	if len(client) != clientSeqSpace || len(discovery) != discoverySeqSpace {
		t.Errorf("the random inputs reach %d client and %d discovery numbers, want %d and %d (all of each range)", len(client), len(discovery), clientSeqSpace, discoverySeqSpace)
	}
	for seq := range discovery {
		if client[seq] {
			t.Fatalf("sequence number %d is drawn by both a client and a discovery", seq)
		}
		if seq < 1000 || seq > 1999 {
			t.Fatalf("discovery sequence number %d is outside 1000-1999", seq)
		}
	}
}

// A client counting up never leaves its range, including across the wrap, so it cannot walk into
// the numbers discovery uses.
func TestClientSequenceStaysInItsRange(t *testing.T) {
	seq := startSeq([2]byte{0x03, 0xe7}) // 999
	for i := 0; i < 3*clientSeqSpace; i++ {
		if seq >= clientSeqSpace {
			t.Fatalf("after %d requests the client's sequence number is %d", i, seq)
		}
		seq = nextSeq(seq)
	}
	if got := nextSeq(999); got != 0 {
		t.Errorf("nextSeq(999) = %d, want the wrap to 0", got)
	}
	if got := nextSeq(41); got != 42 {
		t.Errorf("nextSeq(41) = %d, want 42", got)
	}
}
