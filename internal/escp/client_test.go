package escp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func dialFake(t *testing.T, f *fakeSwitch, pass string) *Client {
	t.Helper()
	c, err := Dial(Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 300 * time.Millisecond,
		SwitchPort: f.port(), HostPort: 0, BroadcastAddr: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestInfo(t *testing.T) {
	c := dialFake(t, startFakeSwitch(t), "secret")
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "TL-SG108E" || info.Hostname != "TL-SG108E-fake" || info.MAC != "00:00:5e:00:53:02" ||
		info.Hardware != "TL-SG108E 6.0" || info.IP != "127.0.0.1" || !info.DHCP {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestLEDRoundTrip(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	on, err := c.GetLED(ctx)
	if err != nil || !on {
		t.Fatalf("GetLED = %v, %v; want true", on, err)
	}
	if err := c.SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	if on, _ := c.GetLED(ctx); on {
		t.Fatal("LED should be off")
	}
	if err := c.SetLED(ctx, true); err != nil {
		t.Fatal(err)
	}
	if led, _, sets := f.state(); !led || sets != 2 {
		t.Fatalf("fake state led=%v sets=%d", led, sets)
	}
}

func TestBadPassword(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "wrong")
	if err := c.SetLED(context.Background(), false); !errors.Is(err, ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
	if led, _, sets := f.state(); !led || sets != 0 {
		t.Fatal("switch must be untouched after a rejected SET")
	}
}

func TestRetryOnLostPacket(t *testing.T) {
	f := startFakeSwitch(t)
	f.dropNth = 1 // lose the very first datagram
	c := dialFake(t, f, "secret")
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatalf("expected retry to recover: %v", err)
	}
}

func TestNoReply(t *testing.T) {
	f := startFakeSwitch(t)
	f.mute = true
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 1
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
}

func TestSessionRecoversAfterSwitchRestart(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if _, err := c.GetLED(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.session = nil // switch forgets the session key
	f.mu.Unlock()
	if _, err := c.GetLED(ctx); err != nil {
		t.Fatalf("client should re-establish the session: %v", err)
	}
}
