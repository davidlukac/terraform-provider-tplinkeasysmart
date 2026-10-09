package escp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func dialFake(t *testing.T, f *fakeSwitch, pass string) *Client {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 300 * time.Millisecond,
		SwitchPort: f.port(), BroadcastAddr: "127.0.0.1"})
	t.Cleanup(func() { c.Close() })
	return c
}

// dialFast is dialFake with short timeouts and a single retry, for tests that
// deliberately make the switch stay silent.
func dialFast(t *testing.T, f *fakeSwitch, pass string) *Client {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 80 * time.Millisecond, Retries: 1,
		SwitchPort: f.port(), BroadcastAddr: "127.0.0.1"})
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
		info.Hardware != "TL-SG108E 6.0" || info.IP != "127.0.0.1" || !info.DHCP || !info.FactoryDefault {
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
	f.dropPacket(1) // lose the very first datagram
	c := dialFake(t, f, "secret")
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatalf("expected retry to recover: %v", err)
	}
}

func TestNoReply(t *testing.T) {
	f := startFakeSwitch(t)
	f.goMute()
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
	f.clearSession() // the switch forgets the session key
	if _, err := c.GetLED(ctx); err != nil {
		t.Fatalf("client should re-establish the session: %v", err)
	}
}

func TestSetHostnameKeepsIPConfiguration(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.SetHostname(context.Background(), "new-name"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hostname != "new-name" || len(f.setIP) != 4 || f.setIP[0] != 127 || f.setIP[3] != 1 {
		t.Fatalf("hostname=%q ip=%v: the current IP must be re-sent unchanged", f.hostname, f.setIP)
	}
	if err := c.SetHostname(context.Background(), "bad name"); err == nil {
		t.Fatal("invalid name must be rejected before any packet is sent")
	}
}

func TestSetPortWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.SetPort(context.Background(), 7, true, "100Mbps-full", true); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	got := f.port7
	f.mu.Unlock()
	want := []byte{7, 1, 0, 5, 0, 1, 0}
	if string(got) != string(want) {
		t.Fatalf("wire bytes %v, want %v", got, want)
	}
	if err := c.SetPort(context.Background(), 7, true, "warp", false); err == nil {
		t.Fatal("unknown speed must be rejected before sending")
	}
	if err := c.SetPort(context.Background(), 0, true, "auto", false); err == nil {
		t.Fatal("port 0 must be rejected")
	}
}
