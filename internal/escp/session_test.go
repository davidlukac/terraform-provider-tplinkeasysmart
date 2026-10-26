package escp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// The fake switch ignores credentials that travel under the static key, so a client that sent its
// password that way would fail here instead of passing unnoticed.
func TestCredentialsMustTravelUnderTheSessionKey(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
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

func TestSessionSetupFailures(t *testing.T) {
	t.Run("switch does not answer discovery", func(t *testing.T) {
		f := escptest.Start(t)
		f.Mute()
		c := dialFast(t, f, "secret")
		if _, err := c.LED(context.Background()); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
	})
	t.Run("switch does not answer the key exchange", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.RSADrop = true })
		c := dialFast(t, f, "secret")
		err := c.SetLED(context.Background(), true)
		if !errors.Is(err, ErrNoReply) || !strings.Contains(err.Error(), "session key exchange") {
			t.Fatalf("want a wrapped ErrNoReply from the key exchange, got %v", err)
		}
		if c.session != nil {
			t.Fatal("a failed key exchange must not leave a session behind")
		}
	})
	t.Run("switch refuses the key", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.RSACode = 77 })
		c := dialFast(t, f, "secret")
		err := c.SetLED(context.Background(), true)
		if err == nil || !strings.Contains(err.Error(), "unexpected error code 77") {
			t.Fatalf("want the refusal code in the error, got %v", err)
		}
		if c.session != nil {
			t.Fatal("a refused key must not leave a session behind")
		}
		// the next call tries again and succeeds once the switch cooperates
		f.Configure(func(f *escptest.Switch) { f.RSACode = 0 })
		if err := c.SetLED(context.Background(), true); err != nil {
			t.Fatalf("retry after the switch recovered: %v", err)
		}
	})
	t.Run("entropy source fails", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFast(t, f, "secret")
		orig := randReader
		randReader = failingReader{}
		defer func() { randReader = orig }()
		if err := c.SetLED(context.Background(), true); err == nil || !strings.Contains(err.Error(), "entropy unavailable") {
			t.Fatalf("want the entropy error, got %v", err)
		}
		if c.session != nil {
			t.Fatal("no session may exist without a key")
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestAuthenticatedRequestFailures(t *testing.T) {
	t.Run("SET gets no answer", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFast(t, f, "secret")
		if _, err := c.LED(context.Background()); err != nil { // establish the session first
			t.Fatal(err)
		}
		f.Configure(func(f *escptest.Switch) { f.DropSets = true })
		if err := c.SetLED(context.Background(), false); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
		if led, _, sets := f.State(); !led || sets != 0 {
			t.Fatal("an unanswered SET must not have been applied by the fake")
		}
	})
	t.Run("GET gets no answer", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFast(t, f, "secret")
		f.Configure(func(f *escptest.Switch) {
			f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) { return nil, 0, true }
		})
		if _, err := c.LED(context.Background()); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
	})
	t.Run("LED reply without the LED TLV", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFast(t, f, "secret")
		f.Configure(func(f *escptest.Switch) {
			f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) {
				return []escptest.Setting{{Type: 99, Value: []byte{1}}}, 0, false
			}
		})
		if _, err := c.LED(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected LED reply") {
			t.Fatalf("want an unexpected-reply error, got %v", err)
		}
	})
	t.Run("LED reply with the wrong length", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFast(t, f, "secret")
		f.Configure(func(f *escptest.Switch) {
			f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) {
				return []escptest.Setting{{Type: wire.TLVLED, Value: []byte{1, 1}}}, 0, false
			}
		})
		if _, err := c.LED(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected LED reply") {
			t.Fatalf("want an unexpected-reply error, got %v", err)
		}
	})
}

func TestWrongPasswordIsRejectedForEverySetter(t *testing.T) {
	setters := map[string]func(*Client, context.Context) error{
		"SetLED": func(c *Client, ctx context.Context) error { return c.SetLED(ctx, true) },
		"SetIGMP": func(c *Client, ctx context.Context) error {
			return c.SetIGMP(ctx, IGMPConfig{Enabled: true, ReportSuppression: false})
		},
		"SetLoopPrevention": func(c *Client, ctx context.Context) error { return c.SetLoopPrevention(ctx, true) },
		"SetPort": func(c *Client, ctx context.Context) error {
			return c.SetPort(ctx, PortConfig{ID: 7, Enabled: true, Speed: "auto", FlowControl: false})
		},
		"SetQoSMode": func(c *Client, ctx context.Context) error { return c.SetQoSMode(ctx, 0) },
		"SetBandwidth": func(c *Client, ctx context.Context) error {
			return c.SetBandwidth(ctx, PortBandwidth{Port: 7, IngressKbps: 64, EgressKbps: 64})
		},
		"SetStormControl": func(c *Client, ctx context.Context) error {
			return c.SetStormControl(ctx, StormConfig{Port: 7, UnknownUnicast: true, Multicast: false, Broadcast: false, RateKbps: 64})
		},
		"SetPortMirror": func(c *Client, ctx context.Context) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 7, IngressPorts: MustPortSet(4)})
		},
		"SetLAG":           func(c *Client, ctx context.Context) error { return c.SetLAG(ctx, 2, MustPortSet(6, 7)) },
		"SetPortBasedVLAN": func(c *Client, ctx context.Context) error { return c.SetPortBasedVLAN(ctx, 2, MustPortSet(6, 7)) },
	}
	for name, call := range setters {
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFast(t, f, "wrong-password")
			if err := call(c, context.Background()); !errors.Is(err, ErrRejected) {
				t.Fatalf("want ErrRejected, got %v", err)
			}
			if got := f.AllSets(); len(got) != 0 {
				t.Fatalf("a rejected SET must not be applied, fake recorded %d", len(got))
			}
		})
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
