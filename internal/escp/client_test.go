package escp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

func dialFake(t *testing.T, f *escptest.Switch, pass string) *Client {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1"})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	return c
}

// dialFast is dialFake with short timeouts and a single retry, for tests that
// deliberately make the switch stay silent.
func dialFast(t *testing.T, f *escptest.Switch, pass string) *Client {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 80 * time.Millisecond, Retries: 1,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1"})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	return c
}

func TestInfo(t *testing.T) {
	c := dialFake(t, escptest.Start(t), "secret")
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
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	on, err := c.LED(ctx)
	if err != nil || !on {
		t.Fatalf("LED = %v, %v; want true", on, err)
	}
	if err := c.SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	if on, _ := c.LED(ctx); on {
		t.Fatal("LED should be off")
	}
	if err := c.SetLED(ctx, true); err != nil {
		t.Fatal(err)
	}
	if led, _, sets := f.State(); !led || sets != 2 {
		t.Fatalf("fake state led=%v sets=%d", led, sets)
	}
}

func TestBadPassword(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "wrong")
	if err := c.SetLED(context.Background(), false); !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
	if led, _, sets := f.State(); !led || sets != 0 {
		t.Fatal("switch must be untouched after a rejected SET")
	}
}

func TestRetryOnLostPacket(t *testing.T) {
	f := escptest.Start(t)
	f.DropFirst(2) // lose both datagrams of the first attempt (the probe goes to the switch and to the broadcast address)
	c := dialFake(t, f, "secret")
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatalf("expected retry to recover: %v", err)
	}
}

func TestNoReply(t *testing.T) {
	f := escptest.Start(t)
	f.Mute()
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 1
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
}

func TestSessionRecoversAfterSwitchRestart(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if _, err := c.LED(ctx); err != nil {
		t.Fatal(err)
	}
	f.ClearSession() // the switch forgets the session key
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("client should re-establish the session: %v", err)
	}
}

func TestSetHostnameKeepsIPConfiguration(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.SetHostname(context.Background(), "new-name"); err != nil {
		t.Fatal(err)
	}
	ip := f.IPSent()
	if f.Hostname() != "new-name" || len(ip) != 4 || ip[0] != 127 || ip[3] != 1 {
		t.Fatalf("hostname=%q ip=%v: the current IP must be re-sent unchanged", f.Hostname(), ip)
	}
	if err := c.SetHostname(context.Background(), "bad name"); err == nil {
		t.Fatal("invalid name must be rejected before any packet is sent")
	}
}

func TestSetPortWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	// 1000Mbps-full: flow control with a fixed 10 or 100 Mbps speed is refused (TestSetPortRefusesFlowControlAtAFixedSpeed).
	if err := c.SetPort(context.Background(), PortConfig{ID: 7, Enabled: true, Speed: Speed1000Full, FlowControl: true}); err != nil {
		t.Fatal(err)
	}
	got := f.Port7Setting()
	want := []byte{7, 1, 0, 6, 0, 1, 0}
	if string(got) != string(want) {
		t.Fatalf("wire bytes %v, want %v", got, want)
	}
	if err := c.SetPort(context.Background(), PortConfig{ID: 7, Enabled: true, Speed: "warp", FlowControl: false}); err == nil {
		t.Fatal("unknown speed must be rejected before sending")
	}
	if err := c.SetPort(context.Background(), PortConfig{ID: 0, Enabled: true, Speed: "auto", FlowControl: false}); err == nil {
		t.Fatal("port 0 must be rejected")
	}
}

func TestRejectedErrorNamesTheCodeAndMatchesErrAuth(t *testing.T) {
	err := error(&RejectedError{Code: 4})
	if !errors.Is(err, ErrRejected) {
		t.Fatal("a rejected write must match ErrRejected")
	}
	if errors.Is(err, ErrNoReply) {
		t.Fatal("a rejected write is not a missing reply")
	}
	msg := err.Error()
	for _, want := range []string{"rejected the request", "error code 4", "refuses this setting"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q must contain %q", msg, want)
		}
	}
	var rejected *RejectedError
	if !errors.As(fmt.Errorf("setting a LAG: %w", err), &rejected) || rejected.Code != 4 {
		t.Fatalf("the code must be recoverable with errors.As, got %+v", rejected)
	}
}

func TestRejectedErrorWithAReasonReplacesTheGenericHint(t *testing.T) {
	err := &RejectedError{Code: 7404, Reason: "a member port has storm control enabled"}
	if !errors.Is(err, ErrRejected) {
		t.Fatal("must still match ErrRejected")
	}
	want := "escp: switch rejected the request (error code 7404): a member port has storm control enabled"
	if err.Error() != want {
		t.Fatalf("got %q, want %q", err.Error(), want)
	}
}

// Admin state and flow control are separate bytes: one set and the other clear tells a swapped
// field apart, which TestSetPortWire (both set) cannot.
func TestSetPortWireStateAndFlowControlAreNotSwapped(t *testing.T) {
	cases := []struct {
		name string
		cfg  PortConfig
		want []byte
	}{
		{"enabled, no flow control", PortConfig{ID: 7, Enabled: true, Speed: SpeedAuto}, []byte{7, 1, 0, 1, 0, 0, 0}},
		{"disabled, flow control", PortConfig{ID: 7, Speed: SpeedAuto, FlowControl: true}, []byte{7, 0, 0, 1, 0, 1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			if err := c.SetPort(context.Background(), tc.cfg); err != nil {
				t.Fatal(err)
			}
			got := f.Port7Setting()
			if string(got) != string(tc.want) {
				t.Fatalf("wire bytes %v, want %v", got, tc.want)
			}
		})
	}
}
