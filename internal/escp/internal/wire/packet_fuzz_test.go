package wire

import (
	"bytes"
	"testing"
)

// Everything below parses or transforms bytes that arrive over the network.
// `go test` runs the seed corpus; run `go test -fuzz=<Name> -fuzztime=30s ./internal/escp/internal/wire`
// to explore further.

// A valid captured reply: header (op 2, seq 59) + TLV 12 (LED on) + terminator.
func seedPacket() []byte {
	return Marshal(Header{Op: OpReply, Seq: 59, SwitchMAC: [6]byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01}}, []TLV{{TLVLED, []byte{1}}})
}

func FuzzUnmarshal(f *testing.F) {
	f.Add(seedPacket())
	f.Add(Marshal(Header{Op: OpSetReply, ErrCode: ErrCodeKeyAccepted, Fragment: 1}, []TLV{{TLVConfigFile, bytes.Repeat([]byte{7}, 300)}}))
	f.Add([]byte{})
	f.Add(make([]byte, HeaderLen))
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, tlvs, err := Unmarshal(data)
		if err != nil {
			return
		}
		if int(h.Length) != len(data) {
			t.Fatalf("accepted a packet whose length field %d differs from its size %d", h.Length, len(data))
		}
		total := 0
		for _, tl := range tlvs {
			total += TLVHeaderLen + len(tl.Value)
		}
		if total > len(data)-HeaderLen {
			t.Fatalf("TLVs claim %d bytes but the payload has only %d", total, len(data)-HeaderLen)
		}
	})
}
