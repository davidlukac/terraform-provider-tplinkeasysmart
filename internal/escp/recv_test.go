package escp

import (
	"testing"
	"time"
)

// An attempt whose deadline has already passed must give up at once instead of
// blocking on the socket: no reply, and no waiting.
func TestRecvWithExpiredDeadlineReturnsImmediately(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.open(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if rep, ok := c.recv(time.Now().Add(-time.Second)); ok || rep.op != 0 || len(rep.tlvs) != 0 {
		t.Fatalf("expected no reply, got %+v ok=%v", rep, ok)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("recv blocked for %v although the deadline had passed", took)
	}
}
