//go:build live

package escp

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"
)

// Manual tests against a real switch. They only read, or write the value that
// is already set. Run with:
//
//	set -a; . ../../../.env; set +a
//	go test -tags live -run Live ./internal/escp -v
func liveClient(t *testing.T) *Client {
	t.Helper()
	c, err := Dial(Config{Host: os.Getenv("SWITCH_01_IP"), Username: os.Getenv("SWITCH_01_USERNAME"),
		Password: os.Getenv("SWITCH_01_PASSWORD"), Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestLiveIGMPSameValueWrite(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	before, err := c.IGMP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("before: %+v", before)
	if err := c.SetIGMP(ctx, before.Enabled, before.ReportSuppression); err != nil {
		t.Fatalf("same-value write rejected: %v", err)
	}
	after, err := c.IGMP(ctx)
	if err != nil || after.Enabled != before.Enabled || after.ReportSuppression != before.ReportSuppression {
		t.Fatalf("state changed by same-value write: %+v err=%v", after, err)
	}
}

func TestLiveLoopPreventionSameValueWrite(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	before, err := c.LoopPrevention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("before: %v", before)
	if err := c.SetLoopPrevention(ctx, before); err != nil {
		t.Fatalf("same-value write rejected: %v", err)
	}
	if after, err := c.LoopPrevention(ctx); err != nil || after != before {
		t.Fatalf("state changed: %v err=%v", after, err)
	}
}

// Real toggles. Each restores the original value unconditionally.
func TestLiveIGMPToggle(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	orig, err := c.IGMP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.SetIGMP(ctx, orig.Enabled, orig.ReportSuppression); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
	}()
	if err := c.SetIGMP(ctx, !orig.Enabled, orig.ReportSuppression); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.IGMP(ctx); got.Enabled == orig.Enabled {
		t.Fatalf("toggle had no effect: %+v", got)
	}
}

func TestLiveLoopPreventionToggle(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	orig, err := c.LoopPrevention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.SetLoopPrevention(ctx, orig); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
	}()
	if err := c.SetLoopPrevention(ctx, !orig); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.LoopPrevention(ctx); got == orig {
		t.Fatal("toggle had no effect")
	}
}

// livePort returns the current state of an unused test port, aborting the test
// if the port is carrying a link or belongs to a LAG.
func livePort(t *testing.T, c *Client, id int) Port {
	t.Helper()
	ports, err := c.Ports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ports {
		if p.ID == id {
			if p.ActualSpeed != "link-down" || p.LAG != 0 {
				t.Fatalf("port %d is in use (%s, LAG %d): refusing to touch it", id, p.ActualSpeed, p.LAG)
			}
			return p
		}
	}
	t.Fatalf("port %d not found", id)
	return Port{}
}

func TestLivePortWrites(t *testing.T) {
	const id = 7
	c, ctx := liveClient(t), context.Background()
	orig := livePort(t, c, id)
	defer func() {
		if err := c.SetPort(ctx, id, orig.Enabled, orig.ConfiguredSpeed, orig.ConfiguredFlowControl); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if now := livePort(t, c, id); now != orig {
			t.Errorf("port %d not restored: %+v vs %+v", id, now, orig)
		}
	}()
	for _, step := range []struct {
		enabled bool
		speed   string
		flow    bool
	}{
		{orig.Enabled, orig.ConfiguredSpeed, !orig.ConfiguredFlowControl},
		{orig.Enabled, "100Mbps-full", orig.ConfiguredFlowControl},
		{!orig.Enabled, orig.ConfiguredSpeed, orig.ConfiguredFlowControl},
	} {
		if err := c.SetPort(ctx, id, step.enabled, step.speed, step.flow); err != nil {
			t.Fatalf("%+v: %v", step, err)
		}
		got := livePort(t, c, id)
		if got.Enabled != step.enabled || got.ConfiguredSpeed != step.speed || got.ConfiguredFlowControl != step.flow {
			t.Fatalf("step %+v not applied: %+v", step, got)
		}
	}
}

func TestLiveQoS(t *testing.T) {
	const id = 7
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, id) // abort unless port 7 is unused
	orig, err := c.QoS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var origPrio int
	for _, p := range orig.Priorities {
		if p.Port == id {
			origPrio = p.Priority
		}
	}
	defer func() {
		if err := c.SetPortPriorities(ctx, map[int]int{id: origPrio}); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if err := c.SetQoSMode(ctx, orig.Mode); err != nil {
			t.Errorf("RESTORE MODE FAILED: %v", err)
		}
		if now, _ := c.QoS(ctx); !reflect.DeepEqual(now, orig) {
			t.Errorf("not restored: %+v vs %+v", now, orig)
		}
	}()
	if err := c.SetQoSMode(ctx, orig.Mode); err != nil { // same-value
		t.Fatalf("same-value mode write rejected: %v", err)
	}
	// Per-port priorities only take effect in port-based mode (0). With every
	// port at priority 1 that is equivalent to the current traffic handling.
	if err := c.SetQoSMode(ctx, 0); err != nil {
		t.Fatal(err)
	}
	want := origPrio%4 + 1
	if err := c.SetPortPriorities(ctx, map[int]int{id: want}); err != nil {
		t.Fatal(err)
	}
	now, _ := c.QoS(ctx)
	for _, p := range now.Priorities {
		switch {
		case p.Port == id && p.Priority != want:
			t.Fatalf("port %d priority %d, want %d", id, p.Priority, want)
		case p.Port != id && p.Priority != priorityOf(orig, p.Port):
			t.Fatalf("port %d changed unexpectedly: %d", p.Port, p.Priority)
		}
	}
}

func priorityOf(q QoS, port int) int {
	for _, p := range q.Priorities {
		if p.Port == port {
			return p.Priority
		}
	}
	return -1
}

func bandwidthOf(t *testing.T, c *Client, port int) PortBandwidth {
	t.Helper()
	rows, err := c.Bandwidth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Port == port {
			return r
		}
	}
	t.Fatalf("port %d missing", port)
	return PortBandwidth{}
}

func TestLiveBandwidth(t *testing.T) {
	const id = 7
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, id) // abort unless port 7 is unused
	orig := bandwidthOf(t, c, id)
	defer func() {
		if err := c.SetBandwidth(ctx, id, orig.IngressKbps, orig.EgressKbps); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if now := bandwidthOf(t, c, id); now != orig {
			t.Errorf("not restored: %+v vs %+v", now, orig)
		}
	}()
	if err := c.SetBandwidth(ctx, id, 1024, 2048); err != nil {
		t.Fatal(err)
	}
	if got := bandwidthOf(t, c, id); got.IngressKbps != 1024 || got.EgressKbps != 2048 {
		t.Fatalf("limits not applied: %+v", got)
	}
	for _, r := range mustBandwidth(t, c) {
		if r.Port != id && (r.IngressKbps != 0 || r.EgressKbps != 0) {
			t.Fatalf("port %d was affected: %+v", r.Port, r)
		}
	}
}

func mustBandwidth(t *testing.T, c *Client) []PortBandwidth {
	rows, err := c.Bandwidth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func stormOf(t *testing.T, c *Client, port int) PortStorm {
	t.Helper()
	rows, err := c.StormControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Port == port {
			return r
		}
	}
	t.Fatalf("port %d missing", port)
	return PortStorm{}
}

func TestLiveStormControl(t *testing.T) {
	const id = 7
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, id) // abort unless port 7 is unused
	orig := stormOf(t, c, id)
	defer func() {
		if err := c.SetStormControl(ctx, id, orig.UnknownUnicast, orig.Multicast, orig.Broadcast, orig.RateKbps); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if now := stormOf(t, c, id); now != orig {
			t.Errorf("not restored: %+v vs %+v", now, orig)
		}
	}()
	if err := c.SetStormControl(ctx, id, false, true, true, 1024); err != nil {
		t.Fatal(err)
	}
	got := stormOf(t, c, id)
	if !got.Enabled || !got.Multicast || !got.Broadcast || got.UnknownUnicast || got.RateKbps != 1024 {
		t.Fatalf("not applied: %+v", got)
	}
	rows, _ := c.StormControl(ctx)
	for _, r := range rows {
		if r.Port != id && r.Enabled {
			t.Fatalf("port %d was affected: %+v", r.Port, r)
		}
	}
}

func TestLivePortMirror(t *testing.T) {
	const dest = 7
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, dest) // the destination must be unused
	orig, err := c.PortMirror(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if orig.Enabled {
		t.Fatal("mirroring is already enabled; refusing to touch it")
	}
	defer func() {
		if err := c.SetPortMirror(ctx, orig); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if now, _ := c.PortMirror(ctx); !reflect.DeepEqual(now, orig) {
			t.Errorf("not restored: %+v vs %+v", now, orig)
		}
	}()
	want := Mirror{Enabled: true, MirrorPort: dest, IngressPorts: []int{4}, EgressPorts: []int{}}
	if err := c.SetPortMirror(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, _ := c.PortMirror(ctx)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("not applied: %+v", got)
	}
}

func lagByID(t *testing.T, c *Client, id int) LAG {
	t.Helper()
	lags, err := c.LAGs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lags {
		if l.ID == id {
			return l
		}
	}
	t.Fatalf("LAG%d missing", id)
	return LAG{}
}

// Creates LAG2 from the two unused ports 6 and 7 and removes it again. LAG1
// (the router uplink) is never written.
func TestLiveLAG2(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, 6)
	livePort(t, c, 7)
	lag1 := lagByID(t, c, 1)
	if orig := lagByID(t, c, 2); len(orig.Ports) != 0 {
		t.Fatalf("LAG2 is already in use: %+v; refusing", orig)
	}
	defer func() {
		if err := c.SetLAG(ctx, 2, nil); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if now := lagByID(t, c, 2); len(now.Ports) != 0 {
			t.Errorf("LAG2 not removed: %+v", now)
		}
		if now := lagByID(t, c, 1); !reflect.DeepEqual(now, lag1) {
			t.Errorf("LAG1 changed: %+v vs %+v", now, lag1)
		}
	}()
	if err := c.SetLAG(ctx, 2, []int{6, 7}); err != nil {
		t.Fatal(err)
	}
	if got := lagByID(t, c, 2); !reflect.DeepEqual(got.Ports, []int{6, 7}) {
		t.Fatalf("not applied: %+v", got)
	}
}

func portVLANByID(v PortBasedVLAN, id int) (PortVLAN, bool) {
	for _, g := range v.VLANs {
		if g.ID == id {
			return g, true
		}
	}
	return PortVLAN{}, false
}

// Creates port-based VLAN 2 from the unused ports 6 and 7 and removes it. VLAN
// 1 must stay unchanged; if the firmware reshuffles it, it is repaired.
func TestLivePortBasedVLAN2(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, 6)
	livePort(t, c, 7)
	orig, err := c.PortBasedVLAN(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !orig.Enabled {
		t.Fatal("port-based VLAN mode is not active; refusing")
	}
	vlan1, ok := portVLANByID(orig, 1)
	if !ok || len(vlan1.Ports) != 8 {
		t.Fatalf("VLAN 1 is not the expected all-ports group: %+v; refusing", orig)
	}
	if _, exists := portVLANByID(orig, 2); exists {
		t.Fatal("VLAN 2 already exists; refusing")
	}
	defer func() {
		if err := c.SetPortBasedVLAN(ctx, 2, nil); err != nil {
			t.Errorf("RESTORE FAILED (remove VLAN 2): %v", err)
		}
		now, _ := c.PortBasedVLAN(ctx)
		if g, _ := portVLANByID(now, 1); !reflect.DeepEqual(g, vlan1) {
			t.Errorf("VLAN 1 changed (%+v); repairing", g)
			if err := c.SetPortBasedVLAN(ctx, 1, vlan1.Ports); err != nil {
				t.Errorf("REPAIR FAILED: %v", err)
			}
		}
		if _, exists := portVLANByID(now, 2); exists || !now.Enabled {
			t.Errorf("not restored: %+v", now)
		}
	}()
	if err := c.SetPortBasedVLAN(ctx, 2, []int{6, 7}); err != nil {
		t.Fatal(err)
	}
	now, _ := c.PortBasedVLAN(ctx)
	if g, ok := portVLANByID(now, 2); !ok || !reflect.DeepEqual(g.Ports, []int{6, 7}) {
		t.Fatalf("VLAN 2 not applied: %+v", now)
	}
	t.Logf("with VLAN 2: %+v", now.VLANs)
}

// Device description (hostname). Approved by the owner on 2026-10-09. The
// request re-sends the current IP configuration, so these tests verify that
// the address, mask, gateway and DHCP flag are unchanged afterwards.
func TestLiveHostnameSameValueWrite(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	before, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("before: hostname=%s ip=%s dhcp=%v", before.Hostname, before.IP, before.DHCP)
	if err := c.SetHostname(ctx, before.Hostname); err != nil {
		t.Fatalf("same-value write rejected: %v", err)
	}
	after, err := c.Info(ctx)
	if err != nil || after != before {
		t.Fatalf("state changed by same-value write: %+v vs %+v err=%v", after, before, err)
	}
}

func TestLiveHostnameRename(t *testing.T) {
	c, ctx := liveClient(t), context.Background()
	before, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := c.SetHostname(ctx, before.Hostname); err != nil {
			t.Errorf("RESTORE FAILED: %v", err)
		}
		if now, _ := c.Info(ctx); now != before {
			t.Errorf("not restored: %+v vs %+v", now, before)
		}
	}()
	temp := before.Hostname + "-t"
	if err := c.SetHostname(ctx, temp); err != nil {
		t.Fatal(err)
	}
	after, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := before
	want.Hostname = temp
	if after != want {
		t.Fatalf("after rename: %+v, want %+v (only the hostname may differ)", after, want)
	}
}

// Two clients (one per switch, as two provider aliases would be) must work at
// the same time on the shared reply port. Needs SWITCH_02_IP; it only sends
// the unauthenticated discovery request to the second switch, no credentials.
func TestLiveTwoClientsShareThePort(t *testing.T) {
	second := os.Getenv("SWITCH_02_IP")
	if second == "" {
		t.Skip("set SWITCH_02_IP to run (discovery only, no credentials are sent to it)")
	}
	a := liveClient(t)
	b, err := Dial(Config{Host: second, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	type result struct {
		info Info
		err  error
	}
	ch := make(chan result, 2)
	go func() { i, e := a.Info(context.Background()); ch <- result{i, e} }()
	go func() { i, e := b.Info(context.Background()); ch <- result{i, e} }()
	macs := map[string]bool{}
	for i := 0; i < 2; i++ {
		r := <-ch
		if r.err != nil {
			t.Fatalf("a client failed while sharing the port: %v", r.err)
		}
		macs[r.info.MAC] = true
	}
	if len(macs) != 2 {
		t.Fatalf("each client must get its own switch's reply, got MACs %v", macs)
	}
}
