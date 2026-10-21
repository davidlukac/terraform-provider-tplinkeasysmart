package escp

import (
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func tlvHex(t *testing.T, typ int16, h string) wire.TLV {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return wire.TLV{Type: typ, Value: b}
}

// Values captured from a TL-SG108E v6 (ports 1+2 in LAG1, ports 1,2,4,8 linked at 1 Gbps).
func TestParsePorts(t *testing.T) {
	got, err := parsePorts([]wire.TLV{
		tlvHex(t, wire.TLVPorts, "01010101060000"),
		tlvHex(t, wire.TLVPorts, "03010001000000"),
		tlvHex(t, wire.TLVPorts, "08010001060000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Port{
		{ID: 1, Enabled: true, LAG: 1, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
		{ID: 3, Enabled: true, LAG: 0, ConfiguredSpeed: "auto", ActualSpeed: "link-down"},
		{ID: 8, Enabled: true, LAG: 0, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParsePortsShort(t *testing.T) {
	if _, err := parsePorts([]wire.TLV{{Type: wire.TLVPorts, Value: []byte{1, 1}}}); err == nil {
		t.Fatal("expected error for short TLV")
	}
}

func TestSpeedCodes(t *testing.T) {
	for i, n := range SpeedNames {
		got, err := speedCode(n)
		if err != nil || int(got) != i+1 || speedName(got) != n {
			t.Errorf("%s: code %d err %v name %s", n, got, err, speedName(got))
		}
	}
	if _, err := speedCode("warp"); err == nil {
		t.Error("expected error for unknown speed")
	}
}
