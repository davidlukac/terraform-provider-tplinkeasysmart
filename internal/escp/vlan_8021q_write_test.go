package escp

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestVLAN8021QEncodeMatchesVendorLayout(t *testing.T) {
	tests := []struct {
		name string
		v    VLAN8021Q
		want []byte
	}{
		// id 2, ports 4 and 5 untagged: member mask 0x18, tagged mask 0, name "iot".
		{"untagged", VLAN8021Q{ID: 2, Name: "iot", Untagged: MustPortSet(4, 5)}, []byte{0, 2, 0, 0, 0, 0x18, 0, 0, 0, 0, 'i', 'o', 't', 0}},
		// id 100, port 1 tagged and port 8 untagged: member 0x81, tagged 0x01, empty name.
		{"mixed", VLAN8021Q{ID: 100, Tagged: MustPortSet(1), Untagged: MustPortSet(8)}, []byte{0, 100, 0, 0, 0, 0x81, 0, 0, 0, 0x01, 0}},
		// highest id, the highest port of the mask.
		{"edges", VLAN8021Q{ID: 4094, Name: "a-b_C9", Tagged: MustPortSet(32)}, []byte{0x0f, 0xfe, 0x80, 0, 0, 0, 0x80, 0, 0, 0, 'a', '-', 'b', '_', 'C', '9', 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.v.encode()
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got % x, want % x", got, tt.want)
			}
		})
	}
}

// What the client writes must read back as the same VLAN through the parser used for GETs.
func TestVLAN8021QEncodeRoundTripsThroughParser(t *testing.T) {
	want := VLAN8021Q{ID: 30, Name: "media", Tagged: MustPortSet(1, 6), Untagged: MustPortSet(2, 3)}
	value := want.encode()
	got, err := parseDot1Q([]wire.TLV{{Type: wire.TLVVLAN8021Q, Value: []byte{1}}, {Type: wire.TLVVLAN8021QVLAN, Value: value}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.VLANs) != 1 || !reflect.DeepEqual(got.VLANs[0], want) {
		t.Fatalf("round trip gave %+v", got.VLANs)
	}
}

func TestVLAN8021QValidation(t *testing.T) {
	tests := []struct {
		name string
		v    VLAN8021Q
		frag string
	}{
		{"id zero", VLAN8021Q{ID: 0, Untagged: MustPortSet(1)}, "invalid VLAN id 0"},
		{"id too big", VLAN8021Q{ID: 4095, Untagged: MustPortSet(1)}, "invalid VLAN id 4095"},
		{"name too long", VLAN8021Q{ID: 2, Name: "elevenchars", Untagged: MustPortSet(1)}, "invalid VLAN name"},
		{"name with space", VLAN8021Q{ID: 2, Name: "a b", Untagged: MustPortSet(1)}, "invalid VLAN name"},
		{"both tagged and untagged", VLAN8021Q{ID: 2, Tagged: MustPortSet(3), Untagged: MustPortSet(3)}, "port 3 is both tagged and untagged"},
		{"no members", VLAN8021Q{ID: 2}, "no member ports"},
	}
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := c.SetVLAN8021Q(context.Background(), tt.v)
			if err == nil || !strings.Contains(err.Error(), tt.frag) {
				t.Fatalf("got %v, want an error containing %q", err, tt.frag)
			}
		})
	}
	if err := c.SetVLAN8021Q(context.Background(), VLAN8021Q{ID: 2}); err == nil {
		t.Fatal("a VLAN without a member port must be rejected")
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("rejected requests reached the switch (%d sets)", n)
	}
}

func TestSetVLAN8021QWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 2, Name: "iot", Untagged: MustPortSet(4, 5)}); err != nil {
		t.Fatal(err)
	}
	got := f.LastSet()
	if len(got) != 1 || got[0].Type != wire.TLVVLAN8021QVLAN || !reflect.DeepEqual(got[0].Value, []byte{0, 2, 0, 0, 0, 0x18, 0, 0, 0, 0, 'i', 'o', 't', 0}) {
		t.Fatalf("wire %+v", got)
	}
	if err := c.DeleteVLAN8021Q(ctx, 2); err != nil {
		t.Fatal(err)
	}
	got = f.LastSet()
	if len(got) != 1 || got[0].Type != wire.TLVVLAN8021QVLAN || !reflect.DeepEqual(got[0].Value, []byte{0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("delete wire %+v", got)
	}
	n := f.WriteCount()
	if c.DeleteVLAN8021Q(ctx, 0) == nil || c.DeleteVLAN8021Q(ctx, 4095) == nil {
		t.Fatal("invalid ids must be rejected")
	}
	if f.WriteCount() != n {
		t.Fatal("rejected deletes must not reach the switch")
	}
}

func TestSetPVIDsWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetPVIDs(ctx, map[PortID]int{5: 300, 4: 2}); err != nil {
		t.Fatal(err)
	}
	got := f.LastSet()
	// One TLV per port, in port order, each port (1 byte) and pvid (2 bytes, big endian).
	want := []escptest.Setting{{Type: wire.TLVVLAN8021QPVID, Value: []byte{4, 0, 2}}, {Type: wire.TLVVLAN8021QPVID, Value: []byte{5, 1, 0x2c}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire %+v, want %+v", got, want)
	}
	n := f.WriteCount()
	for name, bad := range map[string]map[PortID]int{"empty": {}, "port 0": {0: 2}, "port 33": {33: 2}, "pvid 0": {4: 0}, "pvid 4095": {4: 4095}} {
		if c.SetPVIDs(ctx, bad) == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	if f.WriteCount() != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}
