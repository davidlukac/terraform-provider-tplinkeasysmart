package escp

import (
	"context"
	"reflect"
	"testing"
)

func TestParsePortBasedVLAN(t *testing.T) {
	// captured from a TL-SG108E v6 (fw 20230218): mode on, VLAN 1 = ports 1-8, up to 8 VLANs
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

func TestSetPortBasedVLANWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetPortBasedVLAN(ctx, 2, []int{6, 7}); err != nil {
		t.Fatal(err)
	}
	if got := f.set(); got[0].Type != TLVVLANPortBasedVID || !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0x60}) {
		t.Fatalf("wire %+v", got)
	}
	if err := c.SetPortBasedVLAN(ctx, 2, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.set(); !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0}) {
		t.Fatalf("delete wire %v", got[0].Value)
	}
	n := len(f.allSets())
	if c.SetPortBasedVLAN(ctx, 0, []int{1}) == nil || c.SetPortBasedVLAN(ctx, 2, []int{99}) == nil {
		t.Fatal("invalid id or port must be rejected")
	}
	if len(f.allSets()) != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}
