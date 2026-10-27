package escp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseBandwidth(t *testing.T) {
	got, err := parseBandwidth([]wire.TLV{
		tlvHex(t, wire.TLVBandwidthIngress, "010000000000"), tlvHex(t, wire.TLVBandwidthIngress, "020000001000"),
		tlvHex(t, wire.TLVBandwidthEgress, "010000000000"), tlvHex(t, wire.TLVBandwidthEgress, "020000002000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PortBandwidth{{Port: 1}, {Port: 2, IngressKbps: 0x1000, EgressKbps: 0x2000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parseBandwidth([]wire.TLV{{Type: wire.TLVBandwidthIngress, Value: []byte{1}}}); err == nil {
		t.Fatal("expected length error")
	}
}

func TestSetBandwidthWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.SetBandwidth(context.Background(), PortBandwidth{Port: 7, IngressKbps: 1024, EgressKbps: 0}); err != nil {
		t.Fatal(err)
	}
	h := f.AllSets()
	if len(h) != 2 {
		t.Fatalf("want 2 SETs (ingress, egress), got %d", len(h))
	}
	if h[0][0].Type != wire.TLVBandwidthIngress || !reflect.DeepEqual(h[0][0].Value, []byte{7, 1, 0, 0, 4, 0}) {
		t.Fatalf("ingress wire: %+v", h[0])
	}
	if h[1][0].Type != wire.TLVBandwidthEgress || !reflect.DeepEqual(h[1][0].Value, []byte{7, 0, 0, 0, 0, 0}) {
		t.Fatalf("egress wire: %+v", h[1])
	}
	for _, bad := range []Kbps{-64, 100, 2000000} {
		if err := c.SetBandwidth(context.Background(), PortBandwidth{Port: 7, IngressKbps: bad, EgressKbps: 0}); err == nil {
			t.Errorf("rate %d must be rejected", bad)
		}
	}
	if f.WriteCount() != 2 {
		t.Fatal("rejected rates must not reach the switch")
	}
}

func TestBandwidthValidationHappensBeforeAnythingIsSent(t *testing.T) {
	f := escptest.Start(t)
	c := dialFast(t, f, "secret")
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"port 0":                   func() error { return c.SetBandwidth(ctx, PortBandwidth{Port: 0, IngressKbps: 64, EgressKbps: 64}) },
		"port 33":                  func() error { return c.SetBandwidth(ctx, PortBandwidth{Port: 33, IngressKbps: 64, EgressKbps: 64}) },
		"bad ingress":              func() error { return c.SetBandwidth(ctx, PortBandwidth{Port: 7, IngressKbps: 100, EgressKbps: 64}) },
		"bad egress after good in": func() error { return c.SetBandwidth(ctx, PortBandwidth{Port: 7, IngressKbps: 64, EgressKbps: 100}) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("an invalid egress rate must stop the request before the ingress SET is sent, %d SET(s) reached the switch", n)
	}
}

func TestBandwidthPartialFailureIsReportedAndNotHidden(t *testing.T) {
	t.Run("ingress rejected: nothing applied", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom = 1 })
		c := dialFast(t, f, "secret")
		if err := c.SetBandwidth(context.Background(), PortBandwidth{Port: 7, IngressKbps: 64, EgressKbps: 64}); !errors.Is(err, ErrRejected) {
			t.Fatalf("got %v", err)
		}
		if n := f.WriteCount(); n != 0 {
			t.Fatalf("%d SET(s) applied", n)
		}
	})
	t.Run("egress rejected: ingress was applied and the error says so", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom = 2 })
		c := dialFast(t, f, "secret")
		if err := c.SetBandwidth(context.Background(), PortBandwidth{Port: 7, IngressKbps: 64, EgressKbps: 64}); !errors.Is(err, ErrRejected) {
			t.Fatalf("got %v", err)
		}
		if h := f.AllSets(); len(h) != 1 || h[0][0].Type != wire.TLVBandwidthIngress {
			t.Fatalf("exactly the ingress SET should have been applied: %+v", h)
		}
	})
}
