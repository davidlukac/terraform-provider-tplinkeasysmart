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

func TestFakeRowsKeepWhatTheSwitchDecides(t *testing.T) {
	f := escptest.Start(t)
	f.EnableState()
	// port 1 has a link at 1000Mbps full (code 6) and is in LAG 1
	f.EnableRows(4096, []byte{1, 1, 1, 1, 6, 0, 0}, []byte{2, 1, 0, 1, 0, 0, 0})
	c, ctx := dialFake(t, f, escptest.Password), context.Background()

	if err := c.SetPort(ctx, PortConfig{ID: 1, Enabled: true, Speed: SpeedAuto, FlowControl: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPort(ctx, PortConfig{ID: 2, Enabled: true, Speed: Speed100Full}); err != nil {
		t.Fatal(err)
	}
	ports, err := c.Ports(ctx)
	if err != nil || len(ports) != 2 {
		t.Fatalf("Ports = %+v, %v", ports, err)
	}
	if p := ports[0]; p.LAG != 1 || p.ActualSpeed == SpeedLinkDown || !p.ConfiguredFlowControl || !p.ActualFlowControl {
		t.Errorf("port 1 = %+v; want the LAG and the link kept and flow control on", p)
	}
	if p := ports[1]; p.ConfiguredSpeed != Speed100Full || p.ActualSpeed != SpeedLinkDown || p.ActualFlowControl {
		t.Errorf("port 2 = %+v; want the speed written and still no link", p)
	}
}
