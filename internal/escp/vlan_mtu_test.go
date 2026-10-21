package escp

import (
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseMTUVLAN(t *testing.T) {
	off, err := parseMTUVLAN([]wire.TLV{tlvHex(t, wire.TLVVLANMTU, "0001")})
	if err != nil || off != (MTUVLAN{UplinkPort: 1}) {
		t.Fatalf("off: %+v %v", off, err)
	}
	on, err := parseMTUVLAN([]wire.TLV{tlvHex(t, wire.TLVVLANMTU, "0108")})
	if err != nil || on != (MTUVLAN{Enabled: true, UplinkPort: 8}) {
		t.Fatalf("on: %+v %v", on, err)
	}
	if _, err := parseMTUVLAN(nil); err == nil {
		t.Fatal("expected error")
	}
}
