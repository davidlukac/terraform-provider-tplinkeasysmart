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
