package escp

import (
	"context"
	"errors"
	"net"
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
	// before it was processed: only its own two datagrams may be counted.
	if _, err := dialFake(t, f, "secret").Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := f.Received(); n != 2 {
		t.Fatalf("the cancelled call sent datagrams: the switch has received %d, want only the 2 of the later probe", n)
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

// The fake switch ignores credentials that travel under the static key, so a client that sent its
// password that way would fail here instead of passing unnoticed.
func TestCredentialsMustTravelUnderTheSessionKey(t *testing.T) {
	f := escptest.Start(t)
	c := dialFast(t, f, "secret")
	ctx := context.Background()
	if _, err := c.LED(ctx); err != nil { // establishes the session
		t.Fatal(err)
	}
	set := []wire.TLV{
		{Type: wire.TLVUsername, Value: append([]byte("admin"), 0)},
		{Type: wire.TLVPassword, Value: append([]byte("secret"), 0)},
		{Type: wire.TLVLED, Value: []byte{0}},
	}
	c.mu.Lock()
	_, err := c.exchange(ctx, wire.OpSet, set, true, []*net.UDPAddr{c.dest})
	c.mu.Unlock()
	if !errors.Is(err, ErrNoReply) || f.WriteCount() != 0 {
		t.Fatalf("a credentialed SET under the static key must be ignored: err=%v writes=%d", err, f.WriteCount())
	}
	c.mu.Lock()
	_, err = c.exchange(ctx, wire.OpSet, set, false, []*net.UDPAddr{c.dest})
	c.mu.Unlock()
	if err != nil || f.WriteCount() != 1 {
		t.Fatalf("the same SET under the session key must be applied: err=%v writes=%d", err, f.WriteCount())
	}
}

func TestDecryptSessionKeyRefusesAShortValue(t *testing.T) {
	for _, n := range []int{0, 1, 9} {
		if got := escptest.DecryptSessionKey(make([]byte, n)); got != nil {
			t.Errorf("a %d-byte value is no key exchange, got %v", n, got)
		}
	}
}

// A wrong password is a refusal by the switch, for the backup as for every write.
func TestConfigBackupRefusalIsARejection(t *testing.T) {
	f := escptest.Start(t)
	c := dialFast(t, f, "wrong")
	_, err := c.ConfigBackup(context.Background())
	var refused *RejectedError
	if !errors.Is(err, ErrRejected) || !errors.As(err, &refused) || refused.Code != 5 {
		t.Fatalf("want a *RejectedError with code 5, got %v", err)
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
