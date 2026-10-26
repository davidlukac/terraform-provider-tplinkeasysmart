package escp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// A call whose context is already cancelled sends nothing: not the key exchange, not the write.
func TestCancelledContextSendsNothing(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.SetLED(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("the cancelled call wrote %d time(s)", n)
	}
	if led, _, sets := f.State(); !led || sets != 0 {
		t.Fatalf("the switch was touched: led=%v sets=%d", led, sets)
	}
	// Not a datagram reached the switch. A later request, once answered, shows that everything
	// before it was processed, but not that both datagrams of its own probe were counted yet when
	// the first reply came back: so 1 or 2 are its own. A leaked call would have sent a discovery
	// probe first (2 datagrams more), which makes 3 or 4.
	if _, err := dialFake(t, f, "secret").Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.Received(); n > 2 {
		t.Fatalf("the cancelled call sent datagrams: the switch has received %d, want only those of the later probe (at most 2)", n)
	}
}

// A method that sends two writes stops between them when the call is cancelled in the gate of the
// second: the first was applied, the second is not sent.
func TestCancellationBetweenTwoWritesStopsTheSecond(t *testing.T) {
	f := escptest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	var consulted int
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(context.Context, InfoReader, Operation) error {
			if consulted++; consulted == 2 {
				cancel()
			}
			return nil
		}})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	err := c.SetBandwidth(ctx, PortBandwidth{Port: 2, IngressKbps: 64, EgressKbps: 64})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("want the ingress write only, the switch received %d", n)
	}
}

// A call that is waiting for a reply returns when it is cancelled, not when the attempt times out.
func TestCancellationInterruptsTheWaitForAReply(t *testing.T) {
	f := escptest.Start(t)
	f.Mute()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 20 * time.Second,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1"})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	start := time.Now()
	_, err := c.Info(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	// the attempt would have waited 20 s; anything far below that shows the wait was interrupted
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the cancelled call kept waiting for %v", took)
	}
}

// The fragment exchange of the backup also sends nothing for a call that is already done.
func TestExchangeFragmentsSendsNothingForACancelledCall(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if _, err := c.LED(context.Background()); err != nil { // opens the socket and the session
		t.Fatal(err)
	}
	before := f.Received()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.mu.Lock()
	_, err := c.exchangeFragments(ctx, wire.OpSet, []wire.TLV{{Type: wire.TLVConfigFile}}, wire.TLVConfigFile)
	c.mu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if _, err := c.LED(context.Background()); err != nil { // answered, so everything before it was processed
		t.Fatal(err)
	}
	if n := f.Received() - before; n != 1 {
		t.Fatalf("the cancelled exchange sent datagrams: %d arrived since, want only the later read's 1", n)
	}
}
