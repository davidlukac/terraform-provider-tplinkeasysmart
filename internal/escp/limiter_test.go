package escp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// limiterPair reads the ingress limit and the storm control of one port.
func limiterPair(t *testing.T, c *Client, port PortID) (PortBandwidth, PortStorm) {
	t.Helper()
	ctx := context.Background()
	bws, err := c.Bandwidth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	storms, err := c.StormControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var bw PortBandwidth
	var st PortStorm
	for _, b := range bws {
		if b.Port == port {
			bw = b
		}
	}
	for _, s := range storms {
		if s.Port == port {
			st = s
		}
	}
	return bw, st
}

func modelClient(t *testing.T) (*escptest.Switch, *Client) {
	t.Helper()
	f := escptest.Start(t)
	f.EnableModel(nil)
	return f, dialFast(t, f, "secret")
}

// The switch keeps either an ingress limit or storm control on a port (probed on switch 03): the client
// refuses the second one before anything is sent, and says what to do.
func TestIngressLimitIsRefusedWhileStormControlIsOn(t *testing.T) {
	f, c := modelClient(t)
	ctx := context.Background()
	if err := c.SetStormControl(ctx, StormConfig{Port: 3, Broadcast: true, RateKbps: 1024}); err != nil {
		t.Fatal(err)
	}
	writes := f.WriteCount()
	err := c.SetBandwidth(ctx, PortBandwidth{Port: 3, IngressKbps: 10240, EgressKbps: 20480})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	for _, want := range []string{"port 3", "storm control on (1024 kbps)", "10240 kbps would turn storm control off", "Turn storm control off first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}
	if f.WriteCount() != writes {
		t.Fatalf("%d SET(s) reached the switch", f.WriteCount()-writes)
	}
	bw, st := limiterPair(t, c, 3)
	if bw.IngressKbps != 0 || bw.EgressKbps != 0 || !st.Enabled || st.RateKbps != 1024 {
		t.Fatalf("the refused write changed the port: %+v %+v", bw, st)
	}
}

func TestStormControlIsRefusedWhileAnIngressLimitIsSet(t *testing.T) {
	f, c := modelClient(t)
	ctx := context.Background()
	if err := c.SetBandwidth(ctx, PortBandwidth{Port: 3, IngressKbps: 10240, EgressKbps: 20480}); err != nil {
		t.Fatal(err)
	}
	writes := f.WriteCount()
	err := c.SetStormControl(ctx, StormConfig{Port: 3, Multicast: true, RateKbps: 64})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v, want ErrInvalid", err)
	}
	for _, want := range []string{"port 3", "an ingress limit of 10240 kbps", "storm control would remove the limit", "Set the ingress limit to 0 first"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q is not in %q", want, err)
		}
	}
	if f.WriteCount() != writes {
		t.Fatalf("%d SET(s) reached the switch", f.WriteCount()-writes)
	}
	bw, st := limiterPair(t, c, 3)
	if bw.IngressKbps != 10240 || bw.EgressKbps != 20480 || st.Enabled {
		t.Fatalf("the refused write changed the port: %+v %+v", bw, st)
	}
}

// What the switch does not tie together stays writable: the egress limit, an ingress limit of 0, storm control
// switched off, another port.
func TestTheOtherSettingsOfThePortStayWritable(t *testing.T) {
	_, c := modelClient(t)
	ctx := context.Background()
	if err := c.SetStormControl(ctx, StormConfig{Port: 3, Broadcast: true, RateKbps: 1024}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetBandwidth(ctx, PortBandwidth{Port: 3, EgressKbps: 20480}); err != nil {
		t.Fatalf("egress limit and ingress 0 beside storm control: %v", err)
	}
	if err := c.SetBandwidth(ctx, PortBandwidth{Port: 4, IngressKbps: 10240}); err != nil {
		t.Fatalf("ingress limit on another port: %v", err)
	}
	if bw, st := limiterPair(t, c, 3); bw.EgressKbps != 20480 || !st.Enabled {
		t.Fatalf("port 3: %+v %+v", bw, st)
	}
	if err := c.SetStormControl(ctx, StormConfig{Port: 3}); err != nil { // off
		t.Fatalf("storm control off: %v", err)
	}
	if err := c.SetBandwidth(ctx, PortBandwidth{Port: 3, IngressKbps: 10240, EgressKbps: 20480}); err != nil {
		t.Fatalf("ingress limit once storm control is off: %v", err)
	}
	if err := c.SetStormControl(ctx, StormConfig{Port: 3}); err != nil { // off again, beside an ingress limit
		t.Fatalf("storm control off beside an ingress limit: %v", err)
	}
	if bw, st := limiterPair(t, c, 3); bw.IngressKbps != 10240 || st.Enabled {
		t.Fatalf("port 3: %+v %+v", bw, st)
	}
	if err := c.SetStormControl(ctx, StormConfig{Port: 5, Broadcast: true, RateKbps: 64}); err != nil {
		t.Fatalf("storm control on another port: %v", err)
	}
}

// A switch that does not answer the read gets no write: not knowing is not a reason to guess.
func TestNoWriteWhenTheOtherLimiterCannotBeRead(t *testing.T) {
	f := escptest.Start(t) // no model: the tables are not answered
	c := dialFast(t, f, "secret")
	ctx := context.Background()
	if err := c.SetBandwidth(ctx, PortBandwidth{Port: 3, IngressKbps: 64}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("SetBandwidth: got %v, want ErrNoReply", err)
	}
	if err := c.SetStormControl(ctx, StormConfig{Port: 3, Broadcast: true, RateKbps: 64}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("SetStormControl: got %v, want ErrNoReply", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("%d SET(s) reached the switch", n)
	}
}

// The fake switch behaves like the real one (probed on firmware 20250710): the write below, which the client
// would refuse, goes through the low-level path and shows what the switch does with it.
func TestTheFakeSwitchClearsTheOtherLimiterLikeTheRealOne(t *testing.T) {
	_, c := modelClient(t)
	ctx := context.Background()
	storm := wire.TLV{Type: wire.TLVStormControl, Value: []byte{3, 1, 0, 0, 1, 0, 0, 4, 0}}
	ingress, err := bandwidthTLV(wire.TLVBandwidthIngress, 3, 10240)
	if err != nil {
		t.Fatal(err)
	}
	op := Operation{Name: "test", Ports: PortID(3).set(), Impact: ImpactTraffic}
	if err := c.setTLVs(ctx, op, storm); err != nil {
		t.Fatal(err)
	}
	if err := c.setTLVs(ctx, op, ingress); err != nil {
		t.Fatal(err)
	}
	if bw, st := limiterPair(t, c, 3); bw.IngressKbps != 10240 || st.Enabled || st.Broadcast || st.RateKbps != 0 {
		t.Fatalf("an ingress limit must clear storm control: %+v %+v", bw, st)
	}
	if err := c.setTLVs(ctx, op, storm); err != nil {
		t.Fatal(err)
	}
	if bw, st := limiterPair(t, c, 3); bw.IngressKbps != 0 || !st.Enabled {
		t.Fatalf("storm control must clear the ingress limit: %+v %+v", bw, st)
	}
}
