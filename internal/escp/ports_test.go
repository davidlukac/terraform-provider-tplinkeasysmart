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
	for i, n := range ConfigurableSpeeds() {
		got, err := n.code()
		if err != nil || int(got) != i+1 || speedFromCode(got) != n {
			t.Errorf("%s: code %d err %v name %s", n, got, err, speedFromCode(got))
		}
	}
	for _, bad := range []Speed{"warp", "", SpeedLinkDown} {
		if _, err := bad.code(); err == nil {
			t.Errorf("%q: expected an error, it cannot be configured", bad)
		}
	}
}

// The spellings are part of the provider's Terraform state and of the web UI: they must not drift.
func TestSpeedSpellings(t *testing.T) {
	want := []string{"auto", "10Mbps-half", "10Mbps-full", "100Mbps-half", "100Mbps-full", "1000Mbps-full"}
	got := ConfigurableSpeeds()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, s := range got {
		if s.String() != want[i] {
			t.Errorf("speed %d is %q, want %q", i+1, s, want[i])
		}
	}
	if SpeedLinkDown.String() != "link-down" {
		t.Errorf("SpeedLinkDown is %q", SpeedLinkDown)
	}
}

func TestParseSpeed(t *testing.T) {
	for _, s := range ConfigurableSpeeds() {
		if got, err := ParseSpeed(s.String()); err != nil || got != s {
			t.Errorf("ParseSpeed(%q) = %q, %v", s, got, err)
		}
	}
	for _, bad := range []string{"warp", "", "link-down", "AUTO"} {
		if _, err := ParseSpeed(bad); err == nil {
			t.Errorf("ParseSpeed(%q) succeeded", bad)
		}
	}
}

// The returned slice is a copy: a caller cannot change the set the client accepts.
func TestConfigurableSpeedsIsACopy(t *testing.T) {
	s := ConfigurableSpeeds()
	s[0] = "tampered"
	if got := ConfigurableSpeeds()[0]; got != SpeedAuto {
		t.Fatalf("first speed is now %q", got)
	}
}
