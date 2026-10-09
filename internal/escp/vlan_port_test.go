package escp

import (
	"reflect"
	"testing"
)

func TestParsePortBasedVLAN(t *testing.T) {
	// captured from switch 01: mode on, VLAN 1 = ports 1-8, up to 8 VLANs
	got, err := parsePortBasedVLAN([]TLV{
		tlvHex(t, TLVVLANPortBased, "01"), tlvHex(t, TLVVLANPortBasedVID, "01000000ff"), tlvHex(t, TLVVLANPortBasedMax, "08"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := PortBasedVLAN{Enabled: true, MaxVLAN: 8, VLANs: []PortVLAN{{ID: 1, Ports: []int{1, 2, 3, 4, 5, 6, 7, 8}}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parsePortBasedVLAN([]TLV{tlvHex(t, TLVVLANPortBased, "01"), {Type: TLVVLANPortBasedVID, Value: []byte{1}}}); err == nil {
		t.Fatal("expected entry length error")
	}
	if _, err := parsePortBasedVLAN(nil); err == nil {
		t.Fatal("expected missing state error")
	}
}
