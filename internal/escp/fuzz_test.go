package escp

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"net"
	"sort"
	"testing"
)

// Everything below parses or transforms bytes that arrive over the network.
// The fuzz targets assert that no input makes them panic and that the
// properties each function promises hold. `go test` runs the seed corpus; run
// `go test -fuzz=<Name> -fuzztime=30s ./internal/escp` to explore further.

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// A valid captured reply: header (op 2, seq 59) + TLV 12 (LED on) + terminator.
func seedPacket() []byte {
	return marshal(header{Op: OpReply, Seq: 59, SwitchMAC: [6]byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01}}, []TLV{{TLVLED, []byte{1}}})
}

func FuzzUnmarshal(f *testing.F) {
	f.Add(seedPacket())
	f.Add(marshal(header{Op: OpSetReply, ErrCode: ErrCodeKeyAccepted, Fragment: 1}, []TLV{{TLVConfigFile, bytes.Repeat([]byte{7}, 300)}}))
	f.Add([]byte{})
	f.Add(make([]byte, headerLen))
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, tlvs, err := unmarshal(data)
		if err != nil {
			return
		}
		if int(h.Length) != len(data) {
			t.Fatalf("accepted a packet whose length field %d differs from its size %d", h.Length, len(data))
		}
		total := 0
		for _, tl := range tlvs {
			total += tlvHeaderLen + len(tl.Value)
		}
		if total > len(data)-headerLen {
			t.Fatalf("TLVs claim %d bytes but the payload has only %d", total, len(data)-headerLen)
		}
	})
}

// Every settings parser must survive any TLV value of any type.
func FuzzSettingParsers(f *testing.F) {
	f.Add(int16(TLVPorts), mustHex(f, "01010101060000"))
	f.Add(int16(TLVPortStats), mustHex(f, "0101061434f1b7000000002873fb8a00000000"))
	f.Add(int16(TLVLAG), mustHex(f, "0100000003"))
	f.Add(int16(TLVIGMPGroups), mustHex(f, "effffffa000100000009"))
	f.Add(int16(TLVMirror), mustHex(f, "01080000000300000004"))
	f.Add(int16(TLVVLANMTU), mustHex(f, "0001"))
	f.Add(int16(TLVVLANPortBasedVID), mustHex(f, "01000000ff"))
	f.Add(int16(TLVVLAN8021QVLAN), mustHex(f, "000a0000008300000080696f7400"))
	f.Add(int16(TLVVLAN8021QPVID), mustHex(f, "010001"))
	f.Add(int16(TLVQoSPriority), mustHex(f, "0100"))
	f.Add(int16(TLVBandwidthIngress), mustHex(f, "010000000000"))
	f.Add(int16(TLVStormControl), mustHex(f, "010000000000000000"))
	f.Add(int16(TLVLoopPrevention), []byte{1})
	f.Add(int16(TLVPortCount), []byte{8})
	f.Fuzz(func(t *testing.T, typ int16, value []byte) {
		// Fixed types as well as the fuzzed one, so a parser also sees values under its own type.
		for _, ty := range []int16{typ, TLVPorts, TLVPortStats, TLVLAG, TLVIGMPEnabled, TLVIGMPSuppression, TLVIGMPGroups, TLVMirror,
			TLVVLANMTU, TLVVLANPortBased, TLVVLANPortBasedVID, TLVVLANPortBasedMax, TLVVLAN8021Q, TLVVLAN8021QVLAN, TLVVLAN8021QPVID,
			TLVVLAN8021QMax, TLVQoSMode, TLVQoSPriority, TLVBandwidthIngress, TLVBandwidthEgress, TLVStormControl, TLVLoopPrevention,
			TLVPortCount, TLVAutoSave} {
			tl := []TLV{{Type: ty, Value: value}}
			_, _ = parsePorts(tl)
			_, _ = parsePortStats(tl)
			_, _ = parseLAGs(tl)
			_, _ = parseIGMP(tl)
			_, _ = parseMirror(tl)
			_, _ = parseMTUVLAN(tl)
			_, _ = parsePortBasedVLAN(tl)
			_, _ = parseDot1Q(tl)
			_, _ = parseQoS(tl)
			_, _ = parseBandwidth(tl)
			_, _ = parseStorm(tl)
			_, _ = parseLoopPrevention(tl)
			_, _ = parseSystem(tl, tl)
		}
	})
}

func FuzzPortMaskRoundTrip(f *testing.F) {
	f.Add([]byte{1, 2, 3, 8})
	f.Add([]byte{})
	f.Add([]byte{32, 31, 1})
	f.Fuzz(func(t *testing.T, raw []byte) {
		var ports []int
		want := map[int]bool{}
		for _, b := range raw {
			p := int(b)%MaxPort + 1
			ports = append(ports, p)
			want[p] = true
		}
		mask, err := maskFromPorts(ports)
		if err != nil {
			t.Fatalf("ports 1-%d must always be accepted: %v", MaxPort, err)
		}
		got := portsFromMask(mask)
		if len(got) != len(want) || !sort.IntsAreSorted(got) {
			t.Fatalf("round trip of %v gave %v", ports, got)
		}
		for _, p := range got {
			if !want[p] {
				t.Fatalf("port %d appeared from nowhere (%v -> %v)", p, ports, got)
			}
		}
	})
}

// RC4 is its own inverse for both state sizes, whatever the key and data.
func FuzzRC4IsAnInvolution(f *testing.F) {
	f.Add([]byte("hello"), []byte("AbCdEf12"))
	f.Add([]byte{}, []byte("AbCdEf12"))
	f.Fuzz(func(t *testing.T, data, keyIn []byte) {
		key := make([]byte, SessionKeyLen)
		copy(key, keyIn)
		if got := rc4Session(rc4Session(data, key), key); !bytes.Equal(got, data) {
			t.Fatal("session RC4 is not an involution")
		}
		if got := rc4Static(rc4Static(data)); !bytes.Equal(got, data) {
			t.Fatal("static RC4 is not an involution")
		}
	})
}

// Whatever session key the client picks (printable, 8 bytes), the switch's
// private key must recover it from the TLV.
func FuzzRSASessionTLVRoundTrip(f *testing.F) {
	f.Add([]byte("AbCdEf12"))
	f.Add([]byte("zzzzzzzz"))
	f.Add([]byte("00000000"))
	phi := new(big.Int).Mul(big.NewInt(3443843008), big.NewInt(3765816958)) // (p-1)(q-1) of the public modulus
	d := new(big.Int).ModInverse(big.NewInt(rsaE), phi)
	f.Fuzz(func(t *testing.T, raw []byte) {
		key := make([]byte, SessionKeyLen)
		for i := range key {
			c := byte('A')
			if i < len(raw) {
				c = 'A' + raw[i]%58 // letters, which is within the alphabet the client draws from
			}
			key[i] = c
		}
		tl := RSASessionTLV(key)
		if len(tl) != rsaTLVLen || tl[0] != 0 || int(tl[1]) < 1 || int(tl[1]) > rsaWords {
			t.Fatalf("malformed key exchange value %x", tl)
		}
		c := new(big.Int)
		for i := rsaWords - 1; i >= 0; i-- {
			c.Lsh(c, 16)
			c.Or(c, big.NewInt(int64(tl[2+2*i])|int64(tl[3+2*i])<<8))
		}
		m := new(big.Int).Exp(c, d, rsaN)
		if got := reverse(m.FillBytes(make([]byte, SessionKeyLen))); !bytes.Equal(got, key) {
			t.Fatalf("key %q came back as %q", key, got)
		}
	})
}

// A datagram from anywhere must never panic the client's reply filter, and a
// reply that is accepted must echo the request's sequence number.
func FuzzAcceptDatagram(f *testing.F) {
	f.Add(seedPacket(), uint16(59))
	f.Add([]byte{}, uint16(0))
	f.Add(bytes.Repeat([]byte{0xa5}, 200), uint16(1))
	f.Fuzz(func(t *testing.T, data []byte, seq uint16) {
		c, err := Dial(Config{Host: "127.0.0.1"})
		if err != nil {
			t.Fatal(err)
		}
		c.seq = seq
		from := &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}
		if rep, ok := c.accept(from, data); ok && rep.op > OpSetReply {
			t.Fatalf("accepted a reply with invalid op %d", rep.op)
		}
		if _, ok := c.accept(&net.UDPAddr{IP: net.ParseIP("127.0.0.2")}, data); ok {
			t.Fatal("a datagram from another host must never be accepted")
		}
	})
}
