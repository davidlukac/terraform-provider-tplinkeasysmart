package escp

import (
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
