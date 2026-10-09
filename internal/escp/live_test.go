//go:build live

package escp

import (
	"context"
	"os"
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
