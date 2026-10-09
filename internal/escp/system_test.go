package escp

import "testing"

func TestParseSystem(t *testing.T) {
	s, err := parseSystem([]TLV{tlvHex(t, TLVPortCount, "08")}, []TLV{tlvHex(t, TLVAutoSave, "01")})
	if err != nil || s != (System{PortCount: 8, AutoSave: true}) {
		t.Fatalf("got %+v %v", s, err)
	}
	if _, err := parseSystem(nil, []TLV{tlvHex(t, TLVAutoSave, "01")}); err == nil {
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
