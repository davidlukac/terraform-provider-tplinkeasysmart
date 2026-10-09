package escp

import "testing"

func TestParseMTUVLAN(t *testing.T) {
	off, err := parseMTUVLAN([]TLV{tlvHex(t, TLVVLANMTU, "0001")})
	if err != nil || off != (MTUVLAN{UplinkPort: 1}) {
		t.Fatalf("off: %+v %v", off, err)
	}
	on, err := parseMTUVLAN([]TLV{tlvHex(t, TLVVLANMTU, "0108")})
	if err != nil || on != (MTUVLAN{Enabled: true, UplinkPort: 8}) {
		t.Fatalf("on: %+v %v", on, err)
	}
	if _, err := parseMTUVLAN(nil); err == nil {
		t.Fatal("expected error")
	}
}
