package escp

import (
	"context"
	"errors"
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

func TestFakeModelEnforcesTheSwitchRules(t *testing.T) {
	f := escptest.Start(t)
	f.EnableModel(map[int]escptest.LinkSpeed{1: escptest.Link1000Full})
	c, ctx := dialFake(t, f, escptest.Password), context.Background()

	if err := c.SetPort(ctx, PortConfig{ID: 3, Enabled: true, Speed: Speed100Full}); err != nil {
		t.Fatal(err)
	}
	var rejected *RejectedError
	if err := c.SetLAG(ctx, 1, []int{3, 4}); !errors.As(err, &rejected) || rejected.Code != 7410 {
		t.Fatalf("mixed auto and fixed speed: err = %v, want code 7410", err)
	}
	if err := c.SetPort(ctx, PortConfig{ID: 3, Enabled: true, Speed: SpeedAuto}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLAG(ctx, 1, []int{3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPort(ctx, PortConfig{ID: 4, Enabled: true, Speed: Speed100Half}); !errors.As(err, &rejected) || rejected.Code != 7407 {
		t.Fatalf("half duplex on a member: err = %v, want code 7407", err)
	}
	ports, _ := c.Ports(ctx)
	if ports[2].LAG != 1 || ports[3].LAG != 1 {
		t.Errorf("members = %+v, want LAG 1 on ports 3 and 4", ports[2:4])
	}
	if err := c.SetLAG(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	if ports, _ = c.Ports(ctx); ports[2].LAG != 0 {
		t.Errorf("port 3 still in LAG %d after the group was deleted", ports[2].LAG)
	}

	// Port-based VLAN membership is exclusive and a port left in no VLAN returns to VLAN 1.
	if err := c.SetPortBasedVLAN(ctx, 2, []int{3, 4}); err != nil {
		t.Fatal(err)
	}
	vl, _ := c.PortBasedVLAN(ctx)
	if len(vl.VLANs) != 2 || len(vl.VLANs[0].Ports) != 6 || len(vl.VLANs[1].Ports) != 2 {
		t.Fatalf("VLANs = %+v, want VLAN 1 with six ports and VLAN 2 with two", vl.VLANs)
	}
	if err := c.SetPortBasedVLAN(ctx, 2, nil); err != nil {
		t.Fatal(err)
	}
	if vl, _ = c.PortBasedVLAN(ctx); len(vl.VLANs) != 1 || len(vl.VLANs[0].Ports) != 8 {
		t.Errorf("VLANs = %+v, want only VLAN 1 with all eight ports", vl.VLANs)
	}
}
