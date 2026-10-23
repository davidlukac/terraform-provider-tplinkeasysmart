package escp

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// countingPorts scripts a switch that reports n ports (0: no port count in the reply) and counts
// how often the port count was asked for.
func countingPorts(f *escptest.Switch, n int, reads *atomic.Int32) {
	f.Configure(func(f *escptest.Switch) {
		f.GetFn = func(types []int16) ([]escptest.Setting, int32, bool) {
			for _, typ := range types {
				if typ == wire.TLVPortCount {
					reads.Add(1)
					if n == 0 {
						return nil, 0, false
					}
					return []escptest.Setting{{Type: wire.TLVPortCount, Value: []byte{byte(n)}}}, 0, false
				}
			}
			return nil, 0, false
		}
	})
}

// Every writer that names a port refuses one the switch does not have, with ErrInvalid and
// without sending anything. The ports pass ValidatePort (they fit the 32-bit mask), so only the
// switch's own port count can refuse them. The fake is a TL-SG108E: eight ports.
func TestWritersRefusePortsTheSwitchDoesNotHave(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(c *Client) error
	}{
		{"SetPort", func(c *Client) error {
			return c.SetPort(ctx, PortConfig{ID: 9, Enabled: true, Speed: SpeedAuto})
		}},
		{"SetBandwidth", func(c *Client) error { return c.SetBandwidth(ctx, PortBandwidth{Port: 9}) }},
		{"SetStormControl", func(c *Client) error { return c.SetStormControl(ctx, StormConfig{Port: 9}) }},
		{"SetPortMirror destination", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 9, IngressPorts: []int{2}})
		}},
		{"SetPortMirror ingress", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 5, IngressPorts: []int{9}})
		}},
		{"SetPortMirror egress", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 5, EgressPorts: []int{9}})
		}},
		{"SetPortBasedVLAN", func(c *Client) error { return c.SetPortBasedVLAN(ctx, 2, []int{2, 9}) }},
		{"SetVLAN8021Q tagged", func(c *Client) error {
			return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20, Tagged: []int{9}, Untagged: []int{2}})
		}},
		{"SetVLAN8021Q untagged", func(c *Client) error {
			return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20, Untagged: []int{9}})
		}},
		{"SetPVIDs", func(c *Client) error { return c.SetPVIDs(ctx, map[int]int{2: 1, 9: 1}) }},
		{"SetVLANMode", func(c *Client) error {
			return c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 9})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			err := tc.call(c)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if !strings.Contains(err.Error(), "port 9 does not exist: this switch has 8 ports") {
				t.Errorf("the message must name the port and the port count: %v", err)
			}
			if n := f.WriteCount(); n != 0 {
				t.Errorf("nothing may be sent for a port that does not exist, the switch received %d writes", n)
			}
		})
	}
}

// A LAG can only name ports of its own group, so the check is shown on a switch with fewer
// ports than the group allows.
func TestSetLAGRefusesPortsBeyondASmallerSwitch(t *testing.T) {
	f := escptest.Start(t)
	var reads atomic.Int32
	countingPorts(f, 4, &reads)
	c := dialFake(t, f, "secret")
	err := c.SetLAG(context.Background(), 2, []int{5, 6})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "port 5 does not exist: this switch has 4 ports") {
		t.Fatalf("got %v", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("the switch received %d writes", n)
	}
}

// The limit is the switch's, not a fixed one: the last port is accepted, and a larger switch
// accepts ports a smaller one refuses.
func TestPortCountIsTheSwitchsOwn(t *testing.T) {
	ctx := context.Background()
	t.Run("the last port of an eight-port switch is accepted", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFake(t, f, "secret")
		if err := c.SetPort(ctx, PortConfig{ID: 8, Enabled: true, Speed: SpeedAuto}); err != nil {
			t.Fatal(err)
		}
		if n := f.WriteCount(); n != 1 {
			t.Fatalf("want the one write, got %d", n)
		}
	})
	t.Run("a sixteen-port switch accepts port 9", func(t *testing.T) {
		f := escptest.Start(t)
		var reads atomic.Int32
		countingPorts(f, 16, &reads)
		c := dialFake(t, f, "secret")
		if err := c.SetPort(ctx, PortConfig{ID: 9, Enabled: true, Speed: SpeedAuto}); err != nil {
			t.Fatal(err)
		}
		if err := c.SetPort(ctx, PortConfig{ID: 17, Enabled: true, Speed: SpeedAuto}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("port 17 on sixteen ports: %v", err)
		}
	})
}

// The port count is hardware: it is read once per client, however many writes follow, and a
// write that names no port reads nothing.
func TestPortCountIsReadOnce(t *testing.T) {
	ctx := context.Background()
	f := escptest.Start(t)
	var reads atomic.Int32
	countingPorts(f, 8, &reads)
	c := dialFake(t, f, "secret")
	if err := c.SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLAG(ctx, 1, nil); err != nil { // removing a group names no ports
		t.Fatal(err)
	}
	if n := reads.Load(); n != 0 {
		t.Fatalf("no port was named, yet the port count was read %d time(s)", n)
	}
	for _, id := range []int{2, 3, 4} {
		if err := c.SetPort(ctx, PortConfig{ID: id, Enabled: true, Speed: SpeedAuto}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.SetBandwidth(ctx, PortBandwidth{Port: 5}); err != nil {
		t.Fatal(err)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("the port count must be read once and kept, it was read %d times", n)
	}
}

// If the port count cannot be established the write does not happen, and the error says why.
func TestWriteNeedsThePortCount(t *testing.T) {
	ctx := context.Background()
	t.Run("the reply has no port count", func(t *testing.T) {
		f := escptest.Start(t)
		var reads atomic.Int32
		countingPorts(f, 0, &reads)
		c := dialFake(t, f, "secret")
		err := c.SetPort(ctx, PortConfig{ID: 2, Enabled: true, Speed: SpeedAuto})
		if err == nil || !strings.Contains(err.Error(), "did not report its number of ports") {
			t.Fatalf("got %v", err)
		}
		if errors.Is(err, ErrInvalid) {
			t.Errorf("a switch that does not answer is not the caller's mistake: %v", err)
		}
		if n := f.WriteCount(); n != 0 {
			t.Fatalf("the switch received %d writes", n)
		}
		// nothing was remembered: the next write asks again
		_ = c.SetPort(ctx, PortConfig{ID: 2, Enabled: true, Speed: SpeedAuto})
		if n := reads.Load(); n != 2 {
			t.Errorf("a failed read must not be cached, the count was asked %d times", n)
		}
	})
	t.Run("the switch reports zero ports", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) {
			f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) {
				return []escptest.Setting{{Type: wire.TLVPortCount, Value: []byte{0}}}, 0, false
			}
		})
		c := dialFake(t, f, "secret")
		err := c.SetPort(ctx, PortConfig{ID: 1, Enabled: true, Speed: SpeedAuto})
		if err == nil || !strings.Contains(err.Error(), "did not report its number of ports") {
			t.Fatalf("a count of zero is no count, got %v", err)
		}
		if n := f.WriteCount(); n != 0 {
			t.Fatalf("the switch received %d writes", n)
		}
	})
	t.Run("the switch is silent", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) {
			f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) { return nil, 0, true }
		})
		c := dialFast(t, f, "secret")
		err := c.SetPort(ctx, PortConfig{ID: 2, Enabled: true, Speed: SpeedAuto})
		if !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
		if n := f.WriteCount(); n != 0 {
			t.Fatalf("the switch received %d writes", n)
		}
	})
}
