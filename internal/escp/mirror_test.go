package escp

import (
	"reflect"
	"testing"
)

func TestParseMirror(t *testing.T) {
	off, err := parseMirror([]TLV{tlvHex(t, TLVMirror, "00000000000000000000")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(off, Mirror{IngressPorts: []int{}, EgressPorts: []int{}}) {
		t.Fatalf("disabled: %+v", off)
	}
	// layout from the vendor code; mirror port 8, ingress ports 1+2, egress port 3
	on, err := parseMirror([]TLV{tlvHex(t, TLVMirror, "01080000000300000004")})
	if err != nil {
		t.Fatal(err)
	}
	want := Mirror{Enabled: true, MirrorPort: 8, IngressPorts: []int{1, 2}, EgressPorts: []int{3}}
	if !reflect.DeepEqual(on, want) {
		t.Fatalf("enabled: %+v", on)
	}
	if _, err := parseMirror([]TLV{{Type: TLVMirror, Value: make([]byte, 3)}}); err == nil {
		t.Fatal("expected length error")
	}
}
