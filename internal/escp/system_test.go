package escp

import (
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseSystem(t *testing.T) {
	s, err := parseSystem([]wire.TLV{tlvHex(t, wire.TLVPortCount, "08")}, []wire.TLV{tlvHex(t, wire.TLVAutoSave, "01")})
	if err != nil || s != (System{PortCount: 8, AutoSave: true}) {
		t.Fatalf("got %+v %v", s, err)
	}
	if _, err := parseSystem(nil, []wire.TLV{tlvHex(t, wire.TLVAutoSave, "01")}); err == nil {
		t.Fatal("expected incomplete error")
	}
}

func TestValidateHostname(t *testing.T) {
	for _, ok := range []string{"a", "TL-SG108E-01", "sw1"} {
		if err := ValidateHostname(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "has space", "under_score", "this-name-is-way-too-long-for-the-switch-0"} {
		if err := ValidateHostname(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
