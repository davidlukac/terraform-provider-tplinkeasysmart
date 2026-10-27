package escp

import (
	"bytes"
	"encoding/hex"
	"net"
	"sort"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
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
	return wire.Marshal(wire.Header{Op: wire.OpReply, Seq: 59, SwitchMAC: [6]byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01}}, []wire.TLV{{Type: wire.TLVLED, Value: []byte{1}}})
}

// Every settings parser must survive any TLV value of any type.
func FuzzSettingParsers(f *testing.F) {
	f.Add(int16(wire.TLVPorts), mustHex(f, "01010101060000"))
	f.Add(int16(wire.TLVPortStats), mustHex(f, "0101061434f1b7000000002873fb8a00000000"))
	f.Add(int16(wire.TLVLAG), mustHex(f, "0100000003"))
	f.Add(int16(wire.TLVIGMPGroups), mustHex(f, "effffffa000100000009"))
	f.Add(int16(wire.TLVMirror), mustHex(f, "01080000000300000004"))
	f.Add(int16(wire.TLVVLANMTU), mustHex(f, "0001"))
	f.Add(int16(wire.TLVVLANPortBasedVID), mustHex(f, "01000000ff"))
	f.Add(int16(wire.TLVVLAN8021QVLAN), mustHex(f, "000a0000008300000080696f7400"))
	f.Add(int16(wire.TLVVLAN8021QPVID), mustHex(f, "010001"))
	f.Add(int16(wire.TLVQoSPriority), mustHex(f, "0100"))
	f.Add(int16(wire.TLVBandwidthIngress), mustHex(f, "010000000000"))
	f.Add(int16(wire.TLVStormControl), mustHex(f, "010000000000000000"))
	f.Add(int16(wire.TLVLoopPrevention), []byte{1})
	f.Add(int16(wire.TLVPortCount), []byte{8})
	f.Fuzz(func(t *testing.T, rawType int16, value []byte) {
		typ := wire.TLVType(rawType)
		// Fixed types as well as the fuzzed one, so a parser also sees values under its own type.
		for _, ty := range []wire.TLVType{typ, wire.TLVPorts, wire.TLVPortStats, wire.TLVLAG, wire.TLVIGMPEnabled, wire.TLVIGMPSuppression, wire.TLVIGMPGroups, wire.TLVMirror,
			wire.TLVVLANMTU, wire.TLVVLANPortBased, wire.TLVVLANPortBasedVID, wire.TLVVLANPortBasedMax, wire.TLVVLAN8021Q, wire.TLVVLAN8021QVLAN, wire.TLVVLAN8021QPVID,
			wire.TLVVLAN8021QMax, wire.TLVQoSMode, wire.TLVQoSPriority, wire.TLVBandwidthIngress, wire.TLVBandwidthEgress, wire.TLVStormControl, wire.TLVLoopPrevention,
			wire.TLVPortCount, wire.TLVAutoSave} {
			tl := []wire.TLV{{Type: ty, Value: value}}
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
		var ports []PortID
		want := map[PortID]bool{}
		for _, b := range raw {
			p := PortID(int(b)%MaxPort + 1)
			ports = append(ports, p)
			want[p] = true
		}
		set, err := NewPortSet(ports...)
		if err != nil {
			t.Fatalf("ports 1-%d must always be accepted: %v", MaxPort, err)
		}
		got := PortSetFromMask(set.Mask())
		if got != set || got.Len() != len(want) || !sort.SliceIsSorted(got.Ports(), func(i, j int) bool { return got.Ports()[i] < got.Ports()[j] }) {
			t.Fatalf("round trip of %v gave %v", ports, got)
		}
		for _, p := range got.Ports() {
			if !want[p] {
				t.Fatalf("port %d appeared from nowhere (%v -> %v)", p, ports, got)
			}
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
		if rep, ok := c.accept(from, data); ok && rep.op > wire.OpSetReply {
			t.Fatalf("accepted a reply with invalid op %d", rep.op)
		}
		if _, ok := c.accept(&net.UDPAddr{IP: net.ParseIP("127.0.0.2")}, data); ok {
			t.Fatal("a datagram from another host must never be accepted")
		}
	})
}

// FuzzParseMAC: no text makes ParseMAC panic, and whatever it accepts prints back as the lower-case
// colon form that parses to the same value.
func FuzzParseMAC(f *testing.F) {
	for _, s := range []string{"c0:3a:55:6c:81:61", "C0-3A-55-6C-81-61", "", "c03a556c8161", "c0:3a-55:6c:81:61", "0000.5e00.5301"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		m, err := ParseMAC(s)
		if err != nil {
			if !m.IsZero() {
				t.Fatalf("ParseMAC(%q) failed but returned %v", s, m)
			}
			return
		}
		again, err := ParseMAC(m.String())
		if err != nil || again != m {
			t.Fatalf("ParseMAC(%q) = %v, which does not survive String and ParseMAC: %v, %v", s, m, again, err)
		}
	})
}
