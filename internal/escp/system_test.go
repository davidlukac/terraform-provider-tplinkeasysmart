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
