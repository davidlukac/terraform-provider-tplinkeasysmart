package escp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParsePortBasedVLAN(t *testing.T) {
	// captured from a TL-SG108E v6 (fw 20230218): mode on, VLAN 1 = ports 1-8, up to 8 VLANs
	got, err := parsePortBasedVLAN([]wire.TLV{
		tlvHex(t, wire.TLVVLANPortBased, "01"), tlvHex(t, wire.TLVVLANPortBasedVID, "01000000ff"), tlvHex(t, wire.TLVVLANPortBasedMax, "08"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := PortBasedVLAN{Enabled: true, MaxVLAN: 8, VLANs: []PortVLAN{{ID: 1, Ports: MustPortSet(1, 2, 3, 4, 5, 6, 7, 8)}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parsePortBasedVLAN([]wire.TLV{tlvHex(t, wire.TLVVLANPortBased, "01"), {Type: wire.TLVVLANPortBasedVID, Value: []byte{1}}}); err == nil {
		t.Fatal("expected entry length error")
	}
	if _, err := parsePortBasedVLAN(nil); err == nil {
		t.Fatal("expected missing state error")
	}
}

func TestSetPortBasedVLANWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetPortBasedVLAN(ctx, 2, MustPortSet(6, 7)); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); got[0].Type != wire.TLVVLANPortBasedVID || !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0x60}) {
		t.Fatalf("wire %+v", got)
	}
	if err := c.SetPortBasedVLAN(ctx, 2, 0); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0}) {
		t.Fatalf("delete wire %v", got[0].Value)
	}
	n := f.WriteCount()
	if c.SetPortBasedVLAN(ctx, 0, MustPortSet(1)) == nil || c.SetPortBasedVLAN(ctx, 256, MustPortSet(1)) == nil {
		t.Fatal("an invalid id must be rejected")
	}
	if f.WriteCount() != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}

func TestPortVLANID(t *testing.T) {
	for _, g := range []PortVLANID{1, 2, MaxPortVLANID} {
		if err := g.Validate(); err != nil {
			t.Errorf("id %d: %v", g, err)
		}
	}
	for _, g := range []PortVLANID{-1, 0, MaxPortVLANID + 1} {
		if err := g.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("id %d: got %v, want ErrInvalid", g, err)
		}
	}
	if PortVLANID(12).String() != "12" {
		t.Error("an id prints as its number")
	}
}
