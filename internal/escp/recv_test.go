package escp

import (
	"context"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

// An attempt whose deadline has already passed must give up at once instead of
// blocking on the socket: no reply, and no waiting.
// A cancelled call does not wait for the attempt's deadline, even when that is some way off.
func TestRecvWithACancelledContextReturnsImmediately(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.open(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, ok := c.recv(ctx, time.Now().Add(5*time.Second)); ok {
		t.Fatal("nothing was sent, so nothing can have been received")
	}
	if took := time.Since(start); took > 2*time.Second { // the deadline is 5 s away
		t.Fatalf("recv waited %v for a cancelled call", took)
	}
}

func TestRecvWithExpiredDeadlineReturnsImmediately(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.open(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if rep, ok := c.recv(context.Background(), time.Now().Add(-time.Second)); ok || rep.op != 0 || len(rep.tlvs) != 0 {
		t.Fatalf("expected no reply, got %+v ok=%v", rep, ok)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("recv blocked for %v although the deadline had passed", took)
	}
}
