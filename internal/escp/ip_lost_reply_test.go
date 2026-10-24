package escp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

// The fake can only answer from 127.0.0.1, so a "move" here changes the mask and the gateway
// and keeps the address; what tells moved from unchanged is the complete set of three addresses.
var (
	lostReplyBefore = cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.254") // what the fake reports until a write
	lostReplyWanted = cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.2")
)

// The case seen on a real switch: the write was applied, the switch left the old address and the
// reply never arrived. The client used to retry from the key exchange against the old address and
// report "session key exchange: no reply" although the switch had moved.
func TestSetIPConfigSucceedsWhenTheReplyIsLostButTheSwitchMoved(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.OnSystemSet = escptest.SystemSetLeaves })
	c := dialFake(t, f, "secret")
	if err := c.SetIPConfig(context.Background(), lostReplyWanted); err != nil {
		t.Fatalf("the switch has the new settings, so the call must succeed: %v", err)
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("the write must be sent once and not repeated, got %d writes", n)
	}
	if !c.dest.IP.Equal(lostReplyWanted.IP) || c.alsoFrom != nil || c.session != nil {
		t.Fatalf("the client must follow the switch with a fresh session: dest=%v alsoFrom=%v session=%v", c.dest.IP, c.alsoFrom, c.session != nil)
	}
	info, err := c.Info(context.Background())
	if err != nil || info.Netmask != "255.255.0.0" || info.Gateway != "127.0.0.2" {
		t.Fatalf("after the move: %+v err=%v", info, err)
	}
}

// A write that was never applied is not a move: the error says so, and the client stays put.
func TestSetIPConfigReportsAWriteThatWasNotApplied(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.DropSets = true })
	c := dialFast(t, f, "secret")
	err := c.SetIPConfig(context.Background(), lostReplyWanted)
	if !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
	if errors.Is(err, ErrMoveUnconfirmed) {
		t.Fatalf("the state is known (unchanged), so it must not be reported as unconfirmed: %v", err)
	}
	if !strings.Contains(err.Error(), "not applied") || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("the error must say the write was not applied, and where the switch is: %v", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("nothing was applied, got %d writes", n)
	}
	if f.SetsDropped() == 0 {
		t.Fatal("the write never reached the switch, so this test did not exercise a lost write")
	}
	if !c.dest.IP.Equal(lostReplyBefore.IP) || c.alsoFrom != nil {
		t.Fatalf("the client must stay where it was: dest=%v alsoFrom=%v", c.dest.IP, c.alsoFrom)
	}
}

// Applied but unreachable: the state cannot be established, which the caller is told to resolve by
// looking at both addresses.
func TestSetIPConfigReportsAMoveThatCannotBeConfirmed(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.OnSystemSet = escptest.SystemSetVanishes })
	c := dialFast(t, f, "secret")
	err := c.SetIPConfig(context.Background(), lostReplyWanted)
	if !errors.Is(err, ErrMoveUnconfirmed) {
		t.Fatalf("want ErrMoveUnconfirmed, got %v", err)
	}
	if !strings.Contains(err.Error(), "found nothing") {
		t.Errorf("the error must say that a probe found nothing: %v", err)
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("the write was applied once, got %d", n)
	}
	if c.alsoFrom != nil {
		t.Fatal("the client must stop accepting replies from the new address")
	}
}

func TestClassifyLostReply(t *testing.T) {
	const mac = "00:00:5e:00:53:02"
	info := func(mac string, cfg IPConfig) Info {
		return Info{MAC: mac, IP: cfg.IP.String(), Netmask: cfg.Netmask.String(), Gateway: cfg.Gateway.String()}
	}
	third := cfgOf("127.0.0.1", "255.255.255.128", "127.0.0.3")
	cases := []struct {
		name  string
		want  IPConfig
		after Info
		out   lostReplyOutcome
	}{
		{"the new settings", lostReplyWanted, info(mac, lostReplyWanted), lostReplyApplied},
		{"the old settings", lostReplyWanted, info(mac, lostReplyBefore), lostReplyUnchanged},
		{"only the mask differs from the old settings and matches the wanted ones", cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.254"), info(mac, cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.254")), lostReplyApplied},
		{"asked for what was already there, either way the switch is where it should be", lostReplyBefore, info(mac, lostReplyBefore), lostReplyApplied},
		{"settings that are neither", lostReplyWanted, info(mac, third), lostReplyUnexpected},
		{"only the gateway differs", lostReplyWanted, info(mac, cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.3")), lostReplyUnexpected},
		{"a reply without addresses", lostReplyWanted, Info{MAC: mac}, lostReplyUnexpected},
		{"a different device at the new settings", lostReplyWanted, info("aa:bb:cc:00:00:01", lostReplyWanted), lostReplyOtherDevice},
		{"a different device at the old settings", lostReplyWanted, info("aa:bb:cc:00:00:01", lostReplyBefore), lostReplyOtherDevice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyLostReply(mac, lostReplyBefore, tc.want, tc.after); got != tc.out {
				t.Fatalf("outcome %d, want %d", got, tc.out)
			}
		})
	}
}

// The two answers the fake switch cannot give by itself, driven through settleLostReply: another
// device at the address, and a switch with settings that are neither the old nor the new ones.
func TestSettleLostReplyNamesWhatAnswered(t *testing.T) {
	lost := ErrNoReply
	t.Run("a different device answers", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFake(t, f, "secret")
		c.mu.Lock()
		err := c.settleLostReply(context.Background(), "aa:bb:cc:00:00:01", lostReplyBefore, lostReplyWanted, lost)
		c.mu.Unlock()
		if !errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "different device") || !strings.Contains(err.Error(), "00:00:5e:00:53:02") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("the switch reports settings that are neither", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFake(t, f, "secret")
		info, err := c.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		// the fake reports 127.0.0.1/255.255.255.0/127.0.0.254; claim that it used to have something else
		oldOther := IPConfig{IP: net.ParseIP("127.0.0.1"), Netmask: net.ParseIP("255.255.0.0"), Gateway: net.ParseIP("127.0.0.9")}
		c.mu.Lock()
		err = c.settleLostReply(context.Background(), info.MAC, oldOther, lostReplyWanted, lost)
		c.mu.Unlock()
		if !errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "neither the old nor the new") {
			t.Fatalf("got %v", err)
		}
	})
}

// The caller's context ends right after the write was applied: the call must not report
// "cancelled" as if nothing happened. The probe runs anyway and finds the move.
func TestSetIPConfigSettlesWhenTheCallIsCancelledAfterTheWrite(t *testing.T) {
	f := escptest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.Configure(func(f *escptest.Switch) {
		f.OnSystemSet = escptest.SystemSetLeaves
		f.AfterSystemSet = cancel // the switch has applied the write; the caller gives up
	})
	c := dialFake(t, f, "secret")
	if err := c.SetIPConfig(ctx, lostReplyWanted); err != nil {
		t.Fatalf("the switch has the new settings, so the call must succeed: %v", err)
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("the write must be sent once, got %d", n)
	}
	info, err := c.Info(context.Background())
	if err != nil || info.Netmask != "255.255.0.0" || info.Gateway != "127.0.0.2" {
		t.Fatalf("after the move: %+v err=%v", info, err)
	}
}

// Cancelled while the switch still shows the old settings: that proves nothing yet, and the error says so.
func TestSetIPConfigDoesNotClaimTheWriteWasNotAppliedWhenCancelled(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.DropSets = true })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 5 * time.Second,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
		// the gate lets the write go, then the caller gives up before it is sent
		WriteGate: func(context.Context, InfoReader, Operation) error { cancel(); return nil }})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	err := c.SetIPConfig(ctx, lostReplyWanted)
	// The probe sees the old settings, but the call ended before the switch had to answer, so
	// nothing is proven: the error names the cancellation and says the write may yet take effect.
	if !errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "context canceled") || !strings.Contains(err.Error(), "may not have taken effect yet") {
		t.Fatalf("want ErrMoveUnconfirmed naming the cancellation and the doubt, got %v", err)
	}
	if errors.Is(err, ErrNoReply) {
		t.Fatalf("this is not the 'all attempts unanswered' case: %v", err)
	}
}

// A refusal by the switch or by the gate means nothing was applied: no probe, no guessing.
func TestSetIPConfigDoesNotProbeAfterARefusal(t *testing.T) {
	t.Run("the switch refuses", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom = 1 })
		c := dialFake(t, f, "secret")
		err := c.SetIPConfig(context.Background(), lostReplyWanted)
		var refused *RejectedError
		if !errors.As(err, &refused) || strings.Contains(err.Error(), "probe") {
			t.Fatalf("want the refusal as it is, got %v", err)
		}
		if !c.dest.IP.Equal(lostReplyBefore.IP) || c.alsoFrom != nil {
			t.Fatalf("the client must stay where it was: dest=%v alsoFrom=%v", c.dest.IP, c.alsoFrom)
		}
	})
	t.Run("the gate refuses", func(t *testing.T) {
		f := escptest.Start(t)
		c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
			SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
			WriteGate: func(context.Context, InfoReader, Operation) error { return errGateRefused }})
		c.allowLoopback = true
		t.Cleanup(func() { c.Close() })
		if err := c.SetIPConfig(context.Background(), lostReplyWanted); err != errGateRefused { //nolint:errorlint // the contract is the gate's error unchanged
			t.Fatalf("the gate's error must come back unchanged, got %v", err)
		}
		if f.WriteCount() != 0 || c.alsoFrom != nil {
			t.Fatalf("nothing was sent and the client stays: writes=%d alsoFrom=%v", f.WriteCount(), c.alsoFrom)
		}
	})
}
