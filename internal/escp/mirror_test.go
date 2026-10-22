package escp

import (
	"context"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseMirror(t *testing.T) {
	off, err := parseMirror([]wire.TLV{tlvHex(t, wire.TLVMirror, "00000000000000000000")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(off, Mirror{IngressPorts: []int{}, EgressPorts: []int{}}) {
		t.Fatalf("disabled: %+v", off)
	}
	// layout from the vendor code; mirror port 8, ingress ports 1+2, egress port 3
	on, err := parseMirror([]wire.TLV{tlvHex(t, wire.TLVMirror, "01080000000300000004")})
	if err != nil {
		t.Fatal(err)
	}
	want := Mirror{Enabled: true, MirrorPort: 8, IngressPorts: []int{1, 2}, EgressPorts: []int{3}}
	if !reflect.DeepEqual(on, want) {
		t.Fatalf("enabled: %+v", on)
	}
	if _, err := parseMirror([]wire.TLV{{Type: wire.TLVMirror, Value: make([]byte, 3)}}); err == nil {
		t.Fatal("expected length error")
	}
}

func TestSetMirrorWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 7, IngressPorts: []int{4}, EgressPorts: []int{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); !reflect.DeepEqual(got[0].Value, []byte{1, 7, 0, 0, 0, 8, 0, 0, 0, 3}) {
		t.Fatalf("wire %v", got[0].Value)
	}
	if err := c.SetPortMirror(ctx, Mirror{}); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); !reflect.DeepEqual(got[0].Value, make([]byte, 10)) {
		t.Fatalf("disable wire %v", got[0].Value)
	}
	n := len(f.AllSets())
	for _, bad := range []Mirror{
		{Enabled: true, MirrorPort: 7},
		{Enabled: true, MirrorPort: 7, IngressPorts: []int{7}},
		{Enabled: true, MirrorPort: 0, IngressPorts: []int{1}},
		{Enabled: true, MirrorPort: 7, IngressPorts: []int{40}},
		{MirrorPort: 7},
	} {
		if err := c.SetPortMirror(ctx, bad); err == nil {
			t.Errorf("%+v must be rejected", bad)
		}
	}
	if len(f.AllSets()) != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}
