package escp

import (
	"context"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseBandwidth(t *testing.T) {
	got, err := parseBandwidth([]wire.TLV{
		tlvHex(t, wire.TLVBandwidthIngress, "010000000000"), tlvHex(t, wire.TLVBandwidthIngress, "020000001000"),
		tlvHex(t, wire.TLVBandwidthEgress, "010000000000"), tlvHex(t, wire.TLVBandwidthEgress, "020000002000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PortBandwidth{{Port: 1}, {Port: 2, IngressKbps: 0x1000, EgressKbps: 0x2000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parseBandwidth([]wire.TLV{{Type: wire.TLVBandwidthIngress, Value: []byte{1}}}); err == nil {
		t.Fatal("expected length error")
	}
}

func TestSetBandwidthWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.SetBandwidth(context.Background(), PortBandwidth{Port: 7, IngressKbps: 1024, EgressKbps: 0}); err != nil {
		t.Fatal(err)
	}
	h := f.allSets()
	if len(h) != 2 {
		t.Fatalf("want 2 SETs (ingress, egress), got %d", len(h))
	}
	if h[0][0].Type != wire.TLVBandwidthIngress || !reflect.DeepEqual(h[0][0].Value, []byte{7, 1, 0, 0, 4, 0}) {
		t.Fatalf("ingress wire: %+v", h[0])
	}
	if h[1][0].Type != wire.TLVBandwidthEgress || !reflect.DeepEqual(h[1][0].Value, []byte{7, 0, 0, 0, 0, 0}) {
		t.Fatalf("egress wire: %+v", h[1])
	}
	for _, bad := range []int{-64, 100, 2000000} {
		if err := c.SetBandwidth(context.Background(), PortBandwidth{Port: 7, IngressKbps: bad, EgressKbps: 0}); err == nil {
			t.Errorf("rate %d must be rejected", bad)
		}
	}
	if len(f.allSets()) != 2 {
		t.Fatal("rejected rates must not reach the switch")
	}
}
