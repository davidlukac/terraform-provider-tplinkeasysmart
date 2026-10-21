package escp

import (
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseLoopPrevention(t *testing.T) {
	on, err := parseLoopPrevention([]wire.TLV{tlvHex(t, wire.TLVLoopPrevention, "01")})
	if err != nil || !on {
		t.Fatalf("got %v, %v", on, err)
	}
	off, err := parseLoopPrevention([]wire.TLV{tlvHex(t, wire.TLVLoopPrevention, "00")})
	if err != nil || off {
		t.Fatalf("got %v, %v", off, err)
	}
	if _, err := parseLoopPrevention(nil); err == nil {
		t.Fatal("expected error for empty reply")
	}
}
