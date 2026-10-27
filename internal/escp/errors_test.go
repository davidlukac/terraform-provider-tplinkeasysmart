package escp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

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

// Every refusal of a caller's argument matches ErrInvalid and keeps its message. The message
// fragments are what the provider and users have seen so far; they are the contract that the
// change to ErrInvalid did not alter the text.
func TestEveryRefusedArgumentIsErrInvalid(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(c *Client) error
		want string
	}{
		{"PortID.Validate", func(*Client) error { return PortID(0).Validate() }, "invalid port 0 (valid: 1-32)"},
		{"Kbps.Validate", func(*Client) error { return Kbps(100).Validate() }, "100 kbps is not a valid rate"},
		{"ValidateLAG group", func(*Client) error { return ValidateLAG(0, MustPortSet(1, 2)) }, "invalid LAG group 0"},
		{"ValidateLAG port count", func(*Client) error { return ValidateLAG(1, MustPortSet(1)) }, "a LAG takes"},
		{"ValidateLAG port outside its group", func(*Client) error { return ValidateLAG(1, MustPortSet(1, 9)) }, "cannot be in LAG 1"},
		{"ValidateIPConfig not IPv4", func(*Client) error { return ValidateIPConfig(IPConfig{}) }, "must all be IPv4 addresses"},
		{"ValidateIPConfig mask", func(*Client) error {
			return ValidateIPConfig(cfgOf("192.0.2.13", "255.0.255.0", "192.0.2.1"))
		}, "is not a contiguous prefix"},
		{"ValidateIPConfig host address", func(*Client) error {
			return ValidateIPConfig(cfgOf("192.0.2.0", "255.255.255.0", "192.0.2.1"))
		}, "is not a usable host address"},
		{"ValidateIPConfig gateway", func(*Client) error {
			return ValidateIPConfig(cfgOf("192.0.2.13", "255.255.255.0", "192.0.2.13"))
		}, "gateway 192.0.2.13 must be another host address"},
		{"ValidateHostname", func(*Client) error { return ValidateHostname("-bad-") }, "invalid device description"},
		{"VLANID.Validate", func(*Client) error { return VLANID(4095).Validate() }, "invalid VLAN id 4095"},
		{"ValidateVLANName", func(*Client) error { return ValidateVLANName("way-too-long-name") }, "invalid VLAN name"},
		{"ParseSpeed", func(*Client) error { _, err := ParseSpeed("warp"); return err }, `unknown speed "warp"`},
		{"ParseQoSMode", func(*Client) error { _, err := ParseQoSMode("fancy"); return err }, `unknown QoS mode "fancy"`},
		{"Discover window", func(*Client) error {
			_, err := Discover(ctx, DiscoverOptions{Window: -1})
			return err
		}, "negative discovery window"},
		{"SetPort speed", func(c *Client) error { return c.SetPort(ctx, PortConfig{ID: 5, Enabled: true, Speed: "warp"}) }, `unknown speed "warp"`},
		{"SetPort flow control", func(c *Client) error {
			return c.SetPort(ctx, PortConfig{ID: 5, Enabled: true, Speed: Speed100Full, FlowControl: true})
		}, "flow control needs speed"},
		{"SetBandwidth", func(c *Client) error {
			return c.SetBandwidth(ctx, PortBandwidth{Port: 5, IngressKbps: 100})
		}, "invalid rate 100 kbps for port 5"},
		{"SetStormControl rate", func(c *Client) error {
			return c.SetStormControl(ctx, StormConfig{Port: 5, Broadcast: true, RateKbps: 100})
		}, "storm control on port 5 needs a rate"},
		{"SetStormControl type", func(c *Client) error {
			return c.SetStormControl(ctx, StormConfig{Port: 5, RateKbps: 64})
		}, "has a rate (64) but no traffic type"},
		{"SetPortMirror no source", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 5})
		}, "needs at least one ingress or egress"},
		{"SetPortMirror itself", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 5, IngressPorts: MustPortSet(5)})
		}, "cannot mirror itself"},
		{"SetPortMirror disabled with ports", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{MirrorPort: 5})
		}, "a disabled mirror must have no"},
		{"SetLAG", func(c *Client) error { return c.SetLAG(ctx, 1, MustPortSet(1)) }, "a LAG takes"},
		{"SetQoSMode", func(c *Client) error { return c.SetQoSMode(ctx, QoSMode(9)) }, "invalid QoS mode 9"},
		{"SetPortPriorities port", func(c *Client) error { return c.SetPortPriorities(ctx, map[PortID]Priority{9: PriorityLowest}) }, "port 9 does not exist"},
		{"SetPortPriorities priority", func(c *Client) error { return c.SetPortPriorities(ctx, map[PortID]Priority{2: Priority(9)}) }, "invalid priority 9 for port 2"},
		{"SetVLAN8021Q both", func(c *Client) error {
			return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20, Tagged: MustPortSet(2), Untagged: MustPortSet(2)})
		}, "both tagged and untagged"},
		{"SetVLAN8021Q empty", func(c *Client) error { return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20}) }, "has no member ports"},
		{"SetPortBasedVLAN id", func(c *Client) error { return c.SetPortBasedVLAN(ctx, 0, MustPortSet(2, 3)) }, "invalid port-based VLAN id 0"},
		{"SetPVIDs empty", func(c *Client) error { return c.SetPVIDs(ctx, nil) }, "no PVIDs to set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			err := tc.call(c)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the message %q no longer says %q", err, tc.want)
			}
			if errors.Is(err, ErrNoReply) || errors.Is(err, ErrRejected) {
				t.Errorf("an argument error must not look like a switch failure: %v", err)
			}
			if n := f.WriteCount(); n != 0 {
				t.Errorf("a refused argument must send no write, the switch received %d", n)
			}
		})
	}
}

// The class is for the caller's mistakes only: what the switch does, and replies the client cannot
// read, stay what they were.
func TestFailuresOfTheSwitchAreNotErrInvalid(t *testing.T) {
	ctx := context.Background()
	t.Run("a refusal by the switch", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFake(t, f, "wrong")
		if err := c.SetLED(ctx, false); !errors.Is(err, ErrRejected) || errors.Is(err, ErrInvalid) {
			t.Fatalf("want ErrRejected only, got %v", err)
		}
	})
	t.Run("silence", func(t *testing.T) {
		f := escptest.Start(t)
		f.Mute()
		c := dialFast(t, f, "secret")
		if _, err := c.Info(ctx); !errors.Is(err, ErrNoReply) || errors.Is(err, ErrInvalid) {
			t.Fatalf("want ErrNoReply only, got %v", err)
		}
	})
	t.Run("the type matches its sentinel and the sentinel stays apart", func(t *testing.T) {
		if err := (&invalidError{msg: "x"}); !errors.Is(err, ErrInvalid) {
			t.Fatal("the type must match its sentinel")
		}
		if errors.Is(ErrInvalidConfig, ErrInvalid) {
			t.Fatal("ErrInvalidConfig is about a reply, not an argument")
		}
	})
}
