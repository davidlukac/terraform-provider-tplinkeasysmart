package escp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestSetPortPrioritiesStopsWhenTheCurrentStateCannotBeRead(t *testing.T) {
	f := escptest.Start(t)
	c := dialFast(t, f, "secret")
	f.Configure(func(f *escptest.Switch) {
		f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) { return nil, 0, true }
	})
	if err := c.SetPortPriorities(context.Background(), map[PortID]Priority{3: 2}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("got %v", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("priorities must not be written blind: %d SET(s)", n)
	}
}

func TestParseQoS(t *testing.T) {
	// captured from a TL-SG108E v6 (fw 20230218): mode 2 (DSCP/802.1p), all ports raw priority 0 = "1 (lowest)"
	tl := []wire.TLV{tlvHex(t, wire.TLVQoSMode, "02")}
	for p := 1; p <= 3; p++ {
		tl = append(tl, wire.TLV{Type: wire.TLVQoSPriority, Value: []byte{byte(p), 0}})
	}
	tl = append(tl, wire.TLV{Type: wire.TLVQoSPriority, Value: []byte{4, 3}})
	got, err := parseQoS(tl)
	if err != nil {
		t.Fatal(err)
	}
	want := QoS{Mode: QoSDSCP8021p, Priorities: []PortPriority{{1, 1}, {2, 1}, {3, 1}, {4, 4}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parseQoS([]wire.TLV{{Type: wire.TLVQoSMode, Value: []byte{9}}, {Type: wire.TLVQoSPriority, Value: []byte{1}}}); err == nil {
		t.Fatal("expected error for short priority")
	}
	if q, err := parseQoS([]wire.TLV{tlvHex(t, wire.TLVQoSMode, "09")}); err != nil || q.Mode.String() != "unknown(9)" {
		t.Fatalf("unknown mode: %+v %v", q, err)
	}
}

func TestSetQoSWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetQoSMode(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); len(got) != 1 || got[0].Type != wire.TLVQoSMode || got[0].Value[0] != 1 {
		t.Fatalf("mode wire: %+v", got)
	}
	if err := c.SetQoSMode(ctx, 7); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
	if err := c.SetPortPriorities(ctx, map[PortID]Priority{3: 4}); err != nil {
		t.Fatal(err)
	}
	got := f.LastSet()
	if len(got) != 8 {
		t.Fatalf("want all 8 ports re-sent, got %d TLVs", len(got))
	}
	for i, tl := range got {
		want := byte(0)
		if i == 2 {
			want = 3 // port 3 -> priority 4 -> raw 3
		}
		if tl.Type != wire.TLVQoSPriority || tl.Value[0] != byte(i+1) || tl.Value[1] != want {
			t.Fatalf("entry %d: %+v", i, tl)
		}
	}
	if err := c.SetPortPriorities(ctx, map[PortID]Priority{3: 9}); err == nil {
		t.Fatal("priority 9 must be rejected")
	}
	if err := c.SetPortPriorities(ctx, map[PortID]Priority{99: 2}); err == nil {
		t.Fatal("unknown port must be rejected")
	}
}

// The names are part of the provider's Terraform state and of the web UI: they must not drift,
// and the codes are the wire bytes.
func TestQoSModeSpellingsAndCodes(t *testing.T) {
	want := []struct {
		mode QoSMode
		code int
		name string
	}{{QoSPortBased, 0, "port-based"}, {QoS8021p, 1, "802.1p"}, {QoSDSCP8021p, 2, "dscp-802.1p"}}
	got := QoSModes()
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, w := range want {
		if got[i] != w.mode || int(w.mode) != w.code || w.mode.String() != w.name || !w.mode.Valid() {
			t.Errorf("mode %d: %v (%d) %q valid=%v, want code %d name %q", i, got[i], int(got[i]), got[i], got[i].Valid(), w.code, w.name)
		}
		if parsed, err := ParseQoSMode(w.name); err != nil || parsed != w.mode {
			t.Errorf("ParseQoSMode(%q) = %v, %v", w.name, parsed, err)
		}
	}
}

func TestQoSModeUnknownValues(t *testing.T) {
	for _, bad := range []QoSMode{-1, 3, 255} {
		if bad.Valid() {
			t.Errorf("%d must not be valid", int(bad))
		}
	}
	if got := QoSMode(9).String(); got != "unknown(9)" {
		t.Errorf("String of an unknown code = %q", got)
	}
	for _, name := range []string{"", "unknown(9)", "DSCP-802.1P", "dscp"} {
		if _, err := ParseQoSMode(name); err == nil {
			t.Errorf("ParseQoSMode(%q) succeeded", name)
		}
	}
}

// A mode the client does not know is refused before anything is sent, negative values included.
func TestSetQoSModeRefusesUnknownModesWithoutSending(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	n := f.WriteCount()
	for _, bad := range []QoSMode{-1, 3, 256} {
		if err := c.SetQoSMode(context.Background(), bad); err == nil {
			t.Errorf("mode %d accepted", int(bad))
		}
	}
	if f.WriteCount() != n {
		t.Fatal("a refused mode reached the switch")
	}
}

func TestPriorityBounds(t *testing.T) {
	for p, valid := range map[Priority]bool{0: false, 1: true, 2: true, 3: true, 4: true, 5: false, -1: false} {
		if p.Valid() != valid {
			t.Errorf("Priority(%d).Valid() = %v", int(p), p.Valid())
		}
	}
	if PriorityLowest != 1 || PriorityHighest != 4 {
		t.Fatalf("bounds are %d..%d, the web UI shows 1..4", int(PriorityLowest), int(PriorityHighest))
	}
}
