package escp

import (
	"context"
	"reflect"
	"testing"
)

func TestParseBandwidth(t *testing.T) {
	got, err := parseBandwidth([]TLV{
		tlvHex(t, TLVBandwidthIngress, "010000000000"), tlvHex(t, TLVBandwidthIngress, "020000001000"),
		tlvHex(t, TLVBandwidthEgress, "010000000000"), tlvHex(t, TLVBandwidthEgress, "020000002000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PortBandwidth{{Port: 1}, {Port: 2, IngressKbps: 0x1000, EgressKbps: 0x2000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parseBandwidth([]TLV{{Type: TLVBandwidthIngress, Value: []byte{1}}}); err == nil {
		t.Fatal("expected length error")
	}
}

func TestSetBandwidthWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.SetBandwidth(context.Background(), 7, 1024, 0); err != nil {
		t.Fatal(err)
	}
	h := f.allSets()
	if len(h) != 2 {
		t.Fatalf("want 2 SETs (ingress, egress), got %d", len(h))
	}
	if h[0][0].Type != TLVBandwidthIngress || !reflect.DeepEqual(h[0][0].Value, []byte{7, 1, 0, 0, 4, 0}) {
		t.Fatalf("ingress wire: %+v", h[0])
	}
	if h[1][0].Type != TLVBandwidthEgress || !reflect.DeepEqual(h[1][0].Value, []byte{7, 0, 0, 0, 0, 0}) {
		t.Fatalf("egress wire: %+v", h[1])
	}
	for _, bad := range []int{-64, 100, 2000000} {
		if err := c.SetBandwidth(context.Background(), 7, bad, 0); err == nil {
			t.Errorf("rate %d must be rejected", bad)
		}
	}
	if len(f.allSets()) != 2 {
		t.Fatal("rejected rates must not reach the switch")
	}
}
