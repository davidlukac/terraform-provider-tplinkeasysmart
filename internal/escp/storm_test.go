package escp

import (
	"context"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestStormControlRejectsInvalidPortBeforeSending(t *testing.T) {
	f := escptest.Start(t)
	c := dialFast(t, f, "secret")
	for _, port := range []int{0, 33} {
		if err := c.SetStormControl(context.Background(), StormConfig{Port: PortID(port), UnknownUnicast: true, Multicast: false, Broadcast: false, RateKbps: 64}); err == nil {
			t.Errorf("port %d must be rejected", port)
		}
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("%d SET(s) reached the switch", n)
	}
}

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
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetStormControl(ctx, StormConfig{Port: 7, UnknownUnicast: false, Multicast: true, Broadcast: true, RateKbps: 1024}); err != nil {
		t.Fatal(err)
	}
	got := f.LastSet()
	want := []byte{7, 1, 0, 1, 1, 0, 0, 4, 0}
	if len(got) != 1 || got[0].Type != wire.TLVStormControl || !reflect.DeepEqual(got[0].Value, want) {
		t.Fatalf("wire %+v, want %v", got, want)
	}
	if err := c.SetStormControl(ctx, StormConfig{Port: 7, UnknownUnicast: false, Multicast: false, Broadcast: false, RateKbps: 0}); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); !reflect.DeepEqual(got[0].Value, []byte{7, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("disable wire %v", got[0].Value)
	}
	n := f.WriteCount()
	for _, bad := range []struct {
		uu, mc, bc bool
		rate       Kbps
	}{{false, false, true, 0}, {false, false, true, 100}, {false, false, false, 64}, {true, false, false, 2000000}} {
		if err := c.SetStormControl(ctx, StormConfig{Port: 7, UnknownUnicast: bad.uu, Multicast: bad.mc, Broadcast: bad.bc, RateKbps: bad.rate}); err == nil {
			t.Errorf("%+v must be rejected", bad)
		}
	}
	if f.WriteCount() != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}

// Each traffic type lands in its own byte: selecting one at a time tells a swapped field apart,
// which the combined case in TestSetStormWire cannot.
func TestSetStormWireOneTrafficTypeAtATime(t *testing.T) {
	cases := []struct {
		name string
		cfg  StormConfig
		want []byte
	}{
		{"unknown unicast", StormConfig{Port: 5, UnknownUnicast: true, RateKbps: 64}, []byte{5, 1, 1, 0, 0, 0, 0, 0, 64}},
		{"multicast", StormConfig{Port: 5, Multicast: true, RateKbps: 128}, []byte{5, 1, 0, 1, 0, 0, 0, 0, 128}},
		{"broadcast", StormConfig{Port: 5, Broadcast: true, RateKbps: 192}, []byte{5, 1, 0, 0, 1, 0, 0, 0, 192}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			if err := c.SetStormControl(context.Background(), tc.cfg); err != nil {
				t.Fatal(err)
			}
			if got := f.LastSet(); len(got) != 1 || !reflect.DeepEqual(got[0].Value, tc.want) {
				t.Fatalf("wire %+v, want %v", got, tc.want)
			}
		})
	}
}
