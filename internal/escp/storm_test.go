package escp

import (
	"context"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseStorm(t *testing.T) {
	got, err := parseStorm([]wire.TLV{
		tlvHex(t, wire.TLVStormControl, "010000000000000000"), // captured: everything off
		tlvHex(t, wire.TLVStormControl, "0200010100"+"00001388"),
		tlvHex(t, wire.TLVStormControl, "0300010100"+"00000000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// entries 2 and 3: unknown-unicast on, multicast on, broadcast off; entry 3 has no rate so it is not active
	want := []PortStorm{
		{Port: 1},
		{Port: 2, UnknownUnicast: true, Multicast: true, RateKbps: 0x1388, Enabled: true},
		{Port: 3, UnknownUnicast: true, Multicast: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, err := parseStorm([]wire.TLV{{Type: wire.TLVStormControl, Value: []byte{1}}}); err == nil {
		t.Fatal("expected length error")
	}
}

func TestSetStormWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetStormControl(ctx, 7, false, true, true, 1024); err != nil {
		t.Fatal(err)
	}
	got := f.set()
	want := []byte{7, 1, 0, 1, 1, 0, 0, 4, 0}
	if len(got) != 1 || got[0].Type != wire.TLVStormControl || !reflect.DeepEqual(got[0].Value, want) {
		t.Fatalf("wire %+v, want %v", got, want)
	}
	if err := c.SetStormControl(ctx, 7, false, false, false, 0); err != nil {
		t.Fatal(err)
	}
	if got := f.set(); !reflect.DeepEqual(got[0].Value, []byte{7, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("disable wire %v", got[0].Value)
	}
	n := len(f.allSets())
	for _, bad := range []struct {
		uu, mc, bc bool
		rate       int
	}{{false, false, true, 0}, {false, false, true, 100}, {false, false, false, 64}, {true, false, false, 2000000}} {
		if err := c.SetStormControl(ctx, 7, bad.uu, bad.mc, bad.bc, bad.rate); err == nil {
			t.Errorf("%+v must be rejected", bad)
		}
	}
	if len(f.allSets()) != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}
