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
		{"untagged", VLAN8021Q{ID: 2, Name: "iot", Untagged: []int{4, 5}}, []byte{0, 2, 0, 0, 0, 0x18, 0, 0, 0, 0, 'i', 'o', 't', 0}},
		// id 100, port 1 tagged and port 8 untagged: member 0x81, tagged 0x01, empty name.
		{"mixed", VLAN8021Q{ID: 100, Tagged: []int{1}, Untagged: []int{8}}, []byte{0, 100, 0, 0, 0, 0x81, 0, 0, 0, 0x01, 0}},
		// highest id, the highest port of the mask.
		{"edges", VLAN8021Q{ID: 4094, Name: "a-b_C9", Tagged: []int{32}}, []byte{0x0f, 0xfe, 0x80, 0, 0, 0, 0x80, 0, 0, 0, 'a', '-', 'b', '_', 'C', '9', 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.v.encode()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got % x, want % x", got, tt.want)
			}
		})
	}
}

// What the client writes must read back as the same VLAN through the parser used for GETs.
func TestVLAN8021QEncodeRoundTripsThroughParser(t *testing.T) {
	want := VLAN8021Q{ID: 30, Name: "media", Tagged: []int{1, 6}, Untagged: []int{2, 3}}
	value, err := want.encode()
	if err != nil {
		t.Fatal(err)
	}
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
		{"id zero", VLAN8021Q{ID: 0, Untagged: []int{1}}, "invalid VLAN id 0"},
		{"id too big", VLAN8021Q{ID: 4095, Untagged: []int{1}}, "invalid VLAN id 4095"},
		{"name too long", VLAN8021Q{ID: 2, Name: "elevenchars", Untagged: []int{1}}, "invalid VLAN name"},
		{"name with space", VLAN8021Q{ID: 2, Name: "a b", Untagged: []int{1}}, "invalid VLAN name"},
		{"both tagged and untagged", VLAN8021Q{ID: 2, Tagged: []int{3}, Untagged: []int{3}}, "port 3 is both tagged and untagged"},
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
	if err := c.SetVLAN8021Q(context.Background(), VLAN8021Q{ID: 2, Untagged: []int{33}}); err == nil {
		t.Fatal("port 33 does not fit in the mask")
	}
	if err := c.SetVLAN8021Q(context.Background(), VLAN8021Q{ID: 2, Tagged: []int{33}}); err == nil {
		t.Fatal("tagged port 33 does not fit in the mask")
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("rejected requests reached the switch (%d sets)", n)
	}
}

func TestSetVLAN8021QWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 2, Name: "iot", Untagged: []int{4, 5}}); err != nil {
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
	if err := c.SetPVIDs(ctx, map[int]int{5: 300, 4: 2}); err != nil {
		t.Fatal(err)
	}
	got := f.LastSet()
	// One TLV per port, in port order, each port (1 byte) and pvid (2 bytes, big endian).
	want := []escptest.Setting{{Type: wire.TLVVLAN8021QPVID, Value: []byte{4, 0, 2}}, {Type: wire.TLVVLAN8021QPVID, Value: []byte{5, 1, 0x2c}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire %+v, want %+v", got, want)
	}
	n := f.WriteCount()
	for name, bad := range map[string]map[int]int{"empty": {}, "port 0": {0: 2}, "port 33": {33: 2}, "pvid 0": {4: 0}, "pvid 4095": {4: 4095}} {
		if c.SetPVIDs(ctx, bad) == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
	if f.WriteCount() != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}

func TestVLANModeWires(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	steps := []struct {
		name string
		do   func() error
		want escptest.Setting
	}{
		{"802.1Q on", func() error { return c.SetVLAN8021QMode(ctx, true) }, escptest.Setting{Type: wire.TLVVLAN8021Q, Value: []byte{1}}},
		{"802.1Q off", func() error { return c.SetVLAN8021QMode(ctx, false) }, escptest.Setting{Type: wire.TLVVLAN8021Q, Value: []byte{0}}},
		{"port-based on", func() error { return c.SetPortBasedVLANMode(ctx, true) }, escptest.Setting{Type: wire.TLVVLANPortBased, Value: []byte{1}}},
		{"port-based off", func() error { return c.SetPortBasedVLANMode(ctx, false) }, escptest.Setting{Type: wire.TLVVLANPortBased, Value: []byte{0}}},
		{"MTU on, uplink 3", func() error { return c.SetMTUVLAN(ctx, MTUVLAN{Enabled: true, UplinkPort: 3}) }, escptest.Setting{Type: wire.TLVVLANMTU, Value: []byte{1, 3}}},
		{"MTU off ignores the uplink", func() error { return c.SetMTUVLAN(ctx, MTUVLAN{Enabled: false, UplinkPort: 7}) }, escptest.Setting{Type: wire.TLVVLANMTU, Value: []byte{0, 1}}},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if got := f.LastSet(); len(got) != 1 || !reflect.DeepEqual(got[0], s.want) {
			t.Fatalf("%s: wire %+v, want %+v", s.name, got, s.want)
		}
	}
	n := f.WriteCount()
	if c.SetMTUVLAN(ctx, MTUVLAN{Enabled: true, UplinkPort: 0}) == nil || c.SetMTUVLAN(ctx, MTUVLAN{Enabled: true, UplinkPort: 33}) == nil {
		t.Fatal("an invalid uplink port must be rejected")
	}
	if f.WriteCount() != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}
