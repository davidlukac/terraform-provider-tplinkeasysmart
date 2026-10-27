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
	if err := c.SetLAG(ctx, 1, MustPortSet(3, 4)); !errors.As(err, &rejected) || rejected.Code != 7410 {
		t.Fatalf("mixed auto and fixed speed: err = %v, want code 7410", err)
	}
	if err := c.SetPort(ctx, PortConfig{ID: 3, Enabled: true, Speed: SpeedAuto}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLAG(ctx, 1, MustPortSet(3, 4)); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPort(ctx, PortConfig{ID: 4, Enabled: true, Speed: Speed100Half}); !errors.As(err, &rejected) || rejected.Code != 7407 {
		t.Fatalf("half duplex on a member: err = %v, want code 7407", err)
	}
	ports, _ := c.Ports(ctx)
	if ports[2].LAG != 1 || ports[3].LAG != 1 {
		t.Errorf("members = %+v, want LAG 1 on ports 3 and 4", ports[2:4])
	}
	if err := c.SetLAG(ctx, 1, 0); err != nil {
		t.Fatal(err)
	}
	if ports, _ = c.Ports(ctx); ports[2].LAG != 0 {
		t.Errorf("port 3 still in LAG %d after the group was deleted", ports[2].LAG)
	}

	// Port-based VLAN membership is exclusive and a port left in no VLAN returns to VLAN 1.
	if err := c.SetPortBasedVLAN(ctx, 2, MustPortSet(3, 4)); err != nil {
		t.Fatal(err)
	}
	vl, _ := c.PortBasedVLAN(ctx)
	if len(vl.VLANs) != 2 || vl.VLANs[0].Ports.Len() != 6 || vl.VLANs[1].Ports.Len() != 2 {
		t.Fatalf("VLANs = %+v, want VLAN 1 with six ports and VLAN 2 with two", vl.VLANs)
	}
	if err := c.SetPortBasedVLAN(ctx, 2, 0); err != nil {
		t.Fatal(err)
	}
	if vl, _ = c.PortBasedVLAN(ctx); len(vl.VLANs) != 1 || vl.VLANs[0].Ports.Len() != 8 {
		t.Errorf("VLANs = %+v, want only VLAN 1 with all eight ports", vl.VLANs)
	}
}

func TestFakeModelVLANModesAndDot1Q(t *testing.T) {
	f := escptest.Start(t)
	f.EnableModel(nil)
	c, ctx := dialFake(t, f, escptest.Password), context.Background()

	if m, _ := c.VLANMode(ctx); m.Mode != VLANModePortBased {
		t.Fatalf("mode at reset = %v, want port-based", m)
	}
	if err := c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModeDot1Q}); err != nil {
		t.Fatal(err)
	}
	if m, _ := c.VLANMode(ctx); m.Mode != VLANModeDot1Q {
		t.Fatalf("mode = %v, want 802.1q and the others off", m)
	}
	// Two VLANs whose ids share a leading byte must stay apart.
	for _, v := range []VLAN8021Q{{ID: 20, Name: "a", Untagged: MustPortSet(3)}, {ID: 21, Name: "b", Tagged: MustPortSet(4)}} {
		if err := c.SetVLAN8021Q(ctx, v); err != nil {
			t.Fatal(err)
		}
	}
	q, err := c.Dot1Q(ctx)
	if err != nil || len(q.VLANs) != 3 || q.MaxVLAN == 0 || len(q.PVIDs) != 8 {
		t.Fatalf("Dot1Q = %+v, %v; want VLAN 1, 20 and 21, a maximum and eight PVIDs", q, err)
	}
	if err := c.DeleteVLAN8021Q(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if q, _ = c.Dot1Q(ctx); len(q.VLANs) != 2 || q.VLANs[1].ID != 21 {
		t.Errorf("VLANs after the delete = %+v, want VLAN 1 and 21", q.VLANs)
	}
	if err := c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 1}); err != nil {
		t.Fatal(err)
	}
	if m, _ := c.VLANMode(ctx); m.Mode != VLANModeMTU || m.UplinkPort != 1 {
		t.Errorf("mode = %+v, want MTU with uplink 1", m)
	}
}

// The modelled switch has a static address in the documentation range, like switch 03 has a static
// address, so the management-address resource can be exercised without a write that could leave the machine.
func TestFakeModelHasAStaticDocumentationAddress(t *testing.T) {
	f := escptest.Start(t)
	f.EnableModel(nil)
	c := dialFake(t, f, "secret")
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.IP.String() != "192.0.2.13" || info.Netmask.String() != "255.255.255.0" || info.Gateway.String() != "192.0.2.1" || info.DHCP {
		t.Fatalf("got ip %s mask %s gateway %s dhcp %t", info.IP, info.Netmask, info.Gateway, info.DHCP)
	}
}
