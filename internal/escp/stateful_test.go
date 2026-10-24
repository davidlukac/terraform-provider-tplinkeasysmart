package escp

import (
	"context"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

func TestFakeStateRemembersWrites(t *testing.T) {
	f := escptest.Start(t)
	f.EnableState(
		escptest.Setting{Type: 4352, Value: []byte{1}},
		escptest.Setting{Type: 4353},
		escptest.Setting{Type: 4354, Value: []byte{0}},
	)
	c, ctx := dialFake(t, f, escptest.Password), context.Background()

	got, err := c.IGMP(ctx)
	if err != nil || !got.Enabled || got.ReportSuppression || len(got.Groups) != 0 {
		t.Fatalf("seeded IGMP = %+v, %v; want enabled, no suppression, no groups", got, err)
	}
	if err := c.SetIGMP(ctx, IGMPConfig{Enabled: false, ReportSuppression: true}); err != nil {
		t.Fatal(err)
	}
	got, err = c.IGMP(ctx)
	if err != nil || got.Enabled || !got.ReportSuppression {
		t.Errorf("IGMP after the write = %+v, %v; want disabled with suppression", got, err)
	}
	// A type the switch holds nothing for still gets the scripted reply, here the LED.
	if on, err := c.LED(ctx); err != nil || !on {
		t.Errorf("LED = %t, %v; want the scripted default (on)", on, err)
	}
}

func TestFakeWithoutStateKeepsNothing(t *testing.T) {
	f := escptest.Start(t)
	c, ctx := dialFast(t, f, escptest.Password), context.Background()
	if err := c.SetIGMP(ctx, IGMPConfig{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.IGMP(ctx); err == nil {
		t.Error("a switch without state answered a read of a setting it was never given")
	}
}
