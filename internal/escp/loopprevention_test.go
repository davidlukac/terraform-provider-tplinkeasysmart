package escp

import "testing"

func TestParseLoopPrevention(t *testing.T) {
	on, err := parseLoopPrevention([]TLV{tlvHex(t, TLVLoopPrevention, "01")})
	if err != nil || !on {
		t.Fatalf("got %v, %v", on, err)
	}
	off, err := parseLoopPrevention([]TLV{tlvHex(t, TLVLoopPrevention, "00")})
	if err != nil || off {
		t.Fatalf("got %v, %v", off, err)
	}
	if _, err := parseLoopPrevention(nil); err == nil {
		t.Fatal("expected error for empty reply")
	}
}
