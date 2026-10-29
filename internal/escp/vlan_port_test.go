package escp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
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

func TestPortVLANLimitCheck(t *testing.T) {
	for _, c := range []struct {
		name    string
		max     int // the groups the switch reports
		ports   int // its ports
		id      PortVLANID
		wantErr string // empty: accepted
	}{
		{"the default group", 8, 8, 1, ""},
		{"the last group of an 8-port switch", 8, 8, 8, ""},
		{"one past it", 8, 8, 9, "supports ids 1-8"},
		{"the wire maximum is still too high for it", 8, 8, MaxPortVLANID, "supports ids 1-8"},
		{"zero is not an id", 8, 8, 0, "invalid port-based VLAN id 0"},
		{"a 24-port switch that reports 8 groups takes ids up to its port count", 8, 24, 24, ""},
		{"and not beyond", 8, 24, 25, "supports ids 1-24"},
		{"a switch that reports more groups than ports takes them", 32, 24, 32, ""},
		{"and not beyond", 32, 24, 33, "supports ids 1-32"},
		{"a switch that did not report its groups is bounded by its ports", 0, 8, 9, "supports ids 1-8"},
		{"a switch that reported neither takes the wire's range", 0, 0, 200, ""},
		{"and still refuses beyond it", 0, 0, MaxPortVLANID + 1, "invalid port-based VLAN id 256"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := PortBasedVLAN{MaxVLAN: c.max}.GroupLimit(c.ports).Check(c.id)
			if c.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %v, want ErrInvalid containing %q", err, c.wantErr)
			}
		})
	}
}

func TestPortVLANLimit(t *testing.T) {
	for _, c := range []struct {
		groups, ports int
		want          PortVLANLimit
		known         bool
	}{{8, 8, 8, true}, {8, 24, 24, true}, {32, 24, 32, true}, {0, 8, 8, true}, {8, 0, 8, true}, {0, 0, 0, false}} {
		got := NewPortVLANLimit(c.groups, c.ports)
		if got != c.want || got.Known() != c.known {
			t.Errorf("groups %d, ports %d: limit %d (known %t), want %d (known %t)", c.groups, c.ports, got, got.Known(), c.want, c.known)
		}
	}
	if NewPortVLANLimit(8, 24).String() != "24" {
		t.Error("a limit prints as its number")
	}
	if got := (PortBasedVLAN{MaxVLAN: 8}).GroupLimit(24); got != 24 {
		t.Errorf("GroupLimit = %d, want 24", got)
	}
}

func TestPortBasedVLANGroup(t *testing.T) {
	v := PortBasedVLAN{VLANs: []PortVLAN{{ID: 1, Ports: MustPortSet(1, 2)}, {ID: 3, Ports: MustPortSet(5)}}}
	if ports, ok := v.Group(3); !ok || ports != MustPortSet(5) {
		t.Errorf("group 3 = %v, %t", ports, ok)
	}
	if ports, ok := v.Group(2); ok || !ports.IsEmpty() {
		t.Errorf("group 2 = %v, %t, want none", ports, ok)
	}
}

// scriptedPortBased answers the port-based VLAN state with a maximum of max (none when nil) and
// the given port count, and counts the reads of the state.
func scriptedPortBased(max []byte, ports byte, reads *atomic.Int32) func([]wire.TLVType) ([]escptest.Setting, int32, bool) {
	return func(types []wire.TLVType) ([]escptest.Setting, int32, bool) {
		for _, ty := range types {
			switch ty {
			case wire.TLVPortCount:
				return []escptest.Setting{{Type: wire.TLVPortCount, Value: []byte{ports}}}, 0, false
			case wire.TLVVLANPortBased:
				reads.Add(1)
				out := []escptest.Setting{{Type: wire.TLVVLANPortBased, Value: []byte{1}}, {Type: wire.TLVVLANPortBasedVID, Value: []byte{1, 0, 0, 0, 0xff}}}
				if max != nil {
					out = append(out, escptest.Setting{Type: wire.TLVVLANPortBasedMax, Value: max})
				}
				return out, 0, false
			}
		}
		return nil, 0, false
	}
}

func TestSetPortBasedVLANRefusesAGroupTheSwitchDoesNotHave(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name  string
		max   []byte // what the switch reports as the number of groups; nil: nothing
		ports byte   // what it reports as its number of ports
		ok    PortVLANID
		bad   PortVLANID
		want  string
	}{
		{"an 8-port switch reporting 8 groups", []byte{8}, 8, 8, 9, "supports ids 1-8"},
		{"a switch that reports fewer groups than ports is bounded by its ports", []byte{4}, 8, 8, 9, "supports ids 1-8"},
		{"a 24-port switch (the largest of the family) reporting 8 groups", []byte{8}, 24, 24, 25, "supports ids 1-24"},
		{"a switch that reports nothing: the port count bounds it", nil, 8, 8, 9, "supports ids 1-8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := escptest.Start(t)
			var reads atomic.Int32
			f.Configure(func(f *escptest.Switch) { f.GetFn = scriptedPortBased(c.max, c.ports, &reads) })
			cl := dialFake(t, f, "secret")
			err := cl.SetPortBasedVLAN(ctx, c.bad, MustPortSet(2))
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("group %d: got %v, want ErrInvalid containing %q", c.bad, err, c.want)
			}
			if f.WriteCount() != 0 {
				t.Fatalf("the refused write reached the switch (%d writes)", f.WriteCount())
			}
			if err := cl.SetPortBasedVLAN(ctx, c.ok, MustPortSet(2)); err != nil {
				t.Fatalf("group %d: %v", c.ok, err)
			}
			if err := cl.SetPortBasedVLAN(ctx, 2, MustPortSet(3)); err != nil {
				t.Fatal(err)
			}
			if n := reads.Load(); n != 1 {
				t.Errorf("the group limit was read %d times, want once and kept", n)
			}
		})
	}
}

func TestSetPortBasedVLANFailsWhenTheGroupLimitCannotBeRead(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.GetFn = func(types []wire.TLVType) ([]escptest.Setting, int32, bool) { return nil, 1, false } // the switch refuses every read
	})
	c := dialFake(t, f, "secret")
	err := c.SetPortBasedVLAN(context.Background(), 2, MustPortSet(2))
	if err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want the read error, which is not a refusal of the group", err)
	}
	if f.WriteCount() != 0 {
		t.Fatal("nothing may be written when the limit is unknown")
	}
}

func TestSetPortBasedVLANFailsWhenThePortCountCannotBeRead(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.GetFn = func(types []wire.TLVType) ([]escptest.Setting, int32, bool) {
			for _, ty := range types {
				if ty == wire.TLVVLANPortBased { // the state reads fine
					return []escptest.Setting{{Type: wire.TLVVLANPortBased, Value: []byte{1}}, {Type: wire.TLVVLANPortBasedMax, Value: []byte{8}}}, 0, false
				}
			}
			return nil, 1, false // the port count is refused
		}
	})
	c := dialFake(t, f, "secret")
	if err := c.SetPortBasedVLAN(context.Background(), 2, MustPortSet(2)); err == nil || errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want the read error", err)
	}
	if f.WriteCount() != 0 {
		t.Fatal("nothing may be written when the limit is unknown")
	}
}
