package escp

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"
)

var errGateRefused = errors.New("test gate: refused")

// writeCalls maps every method of Client that changes the switch to a call with
// valid arguments, so the call can only fail because the gate says no. The test
// below finds the write methods by reflection and fails when one is missing from
// this table, so a new Set* or Delete* method cannot skip the gate unnoticed.
var writeCalls = map[string]func(context.Context, *Client) error{
	"SetLED":            func(ctx context.Context, c *Client) error { return c.SetLED(ctx, false) },
	"SetBandwidth":      func(ctx context.Context, c *Client) error { return c.SetBandwidth(ctx, 2, 64, 64) },
	"SetIGMP":           func(ctx context.Context, c *Client) error { return c.SetIGMP(ctx, true, false) },
	"SetLAG":            func(ctx context.Context, c *Client) error { return c.SetLAG(ctx, 1, []int{2, 3}) },
	"SetLoopPrevention": func(ctx context.Context, c *Client) error { return c.SetLoopPrevention(ctx, true) },
	"SetPort":           func(ctx context.Context, c *Client) error { return c.SetPort(ctx, 2, true, "auto", false) },
	"SetPortMirror": func(ctx context.Context, c *Client) error {
		return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 2, IngressPorts: []int{3}})
	},
	"SetQoSMode":        func(ctx context.Context, c *Client) error { return c.SetQoSMode(ctx, 1) },
	"SetPortPriorities": func(ctx context.Context, c *Client) error { return c.SetPortPriorities(ctx, map[int]int{2: 1}) },
	"SetStormControl":   func(ctx context.Context, c *Client) error { return c.SetStormControl(ctx, 2, true, false, false, 64) },
	"SetHostname":       func(ctx context.Context, c *Client) error { return c.SetHostname(ctx, "sw-test") },
	"SetVLAN8021Q": func(ctx context.Context, c *Client) error {
		return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 10, Name: "test", Untagged: []int{2}})
	},
	"DeleteVLAN8021Q":      func(ctx context.Context, c *Client) error { return c.DeleteVLAN8021Q(ctx, 10) },
	"SetPVIDs":             func(ctx context.Context, c *Client) error { return c.SetPVIDs(ctx, map[int]int{2: 1}) },
	"SetVLAN8021QMode":     func(ctx context.Context, c *Client) error { return c.SetVLAN8021QMode(ctx, true) },
	"SetPortBasedVLANMode": func(ctx context.Context, c *Client) error { return c.SetPortBasedVLANMode(ctx, true) },
	"SetMTUVLAN":           func(ctx context.Context, c *Client) error { return c.SetMTUVLAN(ctx, true, 8) },
	"SetPortBasedVLAN":     func(ctx context.Context, c *Client) error { return c.SetPortBasedVLAN(ctx, 2, []int{3, 4}) },
	"SetIPConfig": func(ctx context.Context, c *Client) error {
		// the address the fake switch already has, so an allowed write is confirmed at once
		return c.SetIPConfig(ctx, IPConfig{IP: net.IPv4(127, 0, 0, 1), Netmask: net.IPv4(255, 0, 0, 0), Gateway: net.IPv4(127, 0, 0, 2)})
	},
}

func writeMethodNames() []string {
	var names []string
	typ := reflect.TypeOf(&Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		if n := typ.Method(i).Name; strings.HasPrefix(n, "Set") || strings.HasPrefix(n, "Delete") {
			names = append(names, n)
		}
	}
	return names
}

func TestEveryWriteMethodIsInTheGateTable(t *testing.T) {
	names := writeMethodNames()
	if len(names) < 19 {
		t.Fatalf("found only %d write methods by reflection; the scan is broken", len(names))
	}
	for _, n := range names {
		if writeCalls[n] == nil {
			t.Errorf("%s changes the switch but is not in writeCalls: add it, and check that it reaches the write gate", n)
		}
	}
	if len(writeCalls) != len(names) {
		t.Errorf("writeCalls has %d entries for %d write methods; remove the stale ones", len(writeCalls), len(names))
	}
}

// A gate that refuses must stop every write before a single SET is sent, and the
// method returns the gate's error unchanged.
func TestWriteGateRefusalSendsNothingForAnyWriteMethod(t *testing.T) {
	for _, name := range writeMethodNames() {
		call := writeCalls[name]
		if call == nil {
			continue // reported by TestEveryWriteMethodIsInTheGateTable
		}
		t.Run(name, func(t *testing.T) {
			f := startFakeSwitch(t)
			var consulted int
			c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
				SwitchPort: f.port(), BroadcastAddr: "127.0.0.1",
				WriteGate: func(context.Context, InfoReader) error { consulted++; return errGateRefused }})
			c.allowLoopback = true
			t.Cleanup(func() { c.Close() })
			if err := call(context.Background(), c); !errors.Is(err, errGateRefused) {
				t.Fatalf("got %v, want the gate's error", err)
			}
			if consulted == 0 {
				t.Fatal("the gate was not consulted")
			}
			if sets := f.allSets(); len(sets) != 0 {
				t.Fatalf("the switch received %d write(s) despite the refusal: %v", len(sets), sets)
			}
		})
	}
}

// With a gate that allows, the same calls reach the switch, so the test above
// proves the gate and not a broken argument.
func TestWriteGateThatAllowsLetsEveryWriteMethodThrough(t *testing.T) {
	for _, name := range writeMethodNames() {
		call := writeCalls[name]
		if call == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			f := startFakeSwitch(t)
			c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
				SwitchPort: f.port(), BroadcastAddr: "127.0.0.1",
				WriteGate: func(context.Context, InfoReader) error { return nil }})
			c.allowLoopback = true
			t.Cleanup(func() { c.Close() })
			if err := call(context.Background(), c); err != nil {
				t.Fatalf("an allowed write failed: %v", err)
			}
			if len(f.allSets()) == 0 {
				t.Fatal("the write never reached the switch")
			}
		})
	}
}

// Reads are never gated: a gate that refuses everything must not stop a read.
func TestWriteGateDoesNotAffectReads(t *testing.T) {
	f := startFakeSwitch(t)
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(context.Context, InfoReader) error { return errGateRefused }})
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	if _, err := c.Info(ctx); err != nil {
		t.Fatalf("Info: %v", err)
	}
	if _, err := c.GetLED(ctx); err != nil {
		t.Fatalf("GetLED: %v", err)
	}
	if _, err := c.ConfigBackup(ctx); err != nil {
		t.Fatalf("ConfigBackup (an authenticated SET that only reads): %v", err)
	}
}

// The gate reads through the client it guards, so it must run outside the
// client's lock; this would deadlock otherwise.
func TestWriteGateMayReadThroughTheClient(t *testing.T) {
	f := startFakeSwitch(t)
	var firmware string
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(ctx context.Context, sw InfoReader) error {
			info, err := sw.Info(ctx)
			firmware = info.Firmware
			return err
		}})
	t.Cleanup(func() { c.Close() })
	if err := c.SetLED(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if firmware == "" {
		t.Fatal("the gate did not see the firmware version")
	}
}
