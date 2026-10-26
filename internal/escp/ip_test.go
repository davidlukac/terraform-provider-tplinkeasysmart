package escp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
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

// otherDevice is the MAC of a device that is not the fake.
var otherDevice = MAC{0xaa, 0xbb, 0xcc, 0, 0, 1}

func TestClassifyLostReply(t *testing.T) {
	mac := MAC(escptest.DefaultMAC)
	info := func(mac MAC, cfg IPConfig) Info {
		return Info{MAC: mac, IP: cfg.IP.String(), Netmask: cfg.Netmask.String(), Gateway: cfg.Gateway.String()}
	}
	third := cfgOf("127.0.0.1", "255.255.255.128", "127.0.0.3")
	leasing := func(cfg IPConfig) Info { i := info(mac, cfg); i.DHCP = true; return i }
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
		{"the wanted address, but leased: DHCP is still on", lostReplyWanted, leasing(lostReplyWanted), lostReplyUnexpected},
		{"the old settings, still leased", lostReplyWanted, leasing(lostReplyBefore), lostReplyUnchanged},
		{"only the gateway differs", lostReplyWanted, info(mac, cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.3")), lostReplyUnexpected},
		{"a reply without addresses", lostReplyWanted, Info{MAC: mac}, lostReplyUnexpected},
		{"a different device at the new settings", lostReplyWanted, info(otherDevice, lostReplyWanted), lostReplyOtherDevice},
		{"a different device at the old settings", lostReplyWanted, info(otherDevice, lostReplyBefore), lostReplyOtherDevice},
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
		err := c.settleLostReply(context.Background(), otherDevice, lostReplyBefore, lostReplyWanted, lost)
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

func cfgOf(ip, mask, gw string) IPConfig {
	return IPConfig{IP: net.ParseIP(ip), Netmask: net.ParseIP(mask), Gateway: net.ParseIP(gw)}
}

func TestValidateIPConfig(t *testing.T) {
	good := []IPConfig{
		cfgOf("192.0.2.13", "255.255.255.0", "192.0.2.1"),
		cfgOf("192.0.2.14", "255.255.0.0", "192.0.2.2"),
		cfgOf("10.0.0.2", "255.0.0.0", "0.0.0.0"),            // no gateway
		cfgOf("192.0.2.13", "255.255.255.252", "192.0.2.14"), // /30: two hosts
	}
	for _, cfg := range good {
		if err := ValidateIPConfig(cfg); err != nil {
			t.Errorf("%s: %v", cfg, err)
		}
	}
	bad := []struct {
		name string
		cfg  IPConfig
		want string
	}{
		{"missing gateway", IPConfig{IP: net.ParseIP("192.0.2.13"), Netmask: net.ParseIP("255.255.255.0")}, "must all be IPv4"},
		{"ipv6 address", cfgOf("2001:db8::1", "255.255.255.0", "192.0.2.1"), "must all be IPv4"},
		{"gap in mask", cfgOf("192.0.2.13", "255.0.255.0", "192.0.2.1"), "contiguous prefix"},
		{"mask zero", cfgOf("192.0.2.13", "0.0.0.0", "192.0.2.1"), "contiguous prefix"},
		{"mask /7", cfgOf("10.0.0.2", "254.0.0.0", "10.0.0.1"), "contiguous prefix"},
		{"mask /31", cfgOf("192.0.2.13", "255.255.255.254", "192.0.2.12"), "contiguous prefix"},
		{"unspecified address", cfgOf("0.0.0.0", "255.255.255.0", "192.0.2.1"), "not a usable host"},
		{"loopback address", cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2"), "not a usable host"},
		{"multicast address", cfgOf("224.0.0.5", "255.255.255.0", "224.0.0.1"), "not a usable host"},
		{"network address", cfgOf("192.0.2.0", "255.255.255.0", "192.0.2.1"), "not a usable host"},
		{"broadcast address", cfgOf("192.0.2.255", "255.255.255.0", "192.0.2.1"), "not a usable host"},
		{"gateway outside", cfgOf("192.0.2.13", "255.255.255.0", "192.0.3.1"), "gateway"},
		{"gateway is the network", cfgOf("192.0.2.13", "255.255.255.0", "192.0.2.0"), "gateway"},
		{"gateway is the broadcast", cfgOf("192.0.2.13", "255.255.255.0", "192.0.2.255"), "gateway"},
		{"gateway is the switch", cfgOf("192.0.2.13", "255.255.255.0", "192.0.2.13"), "gateway"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIPConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want an error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestIPConfigFromInfoRefusesUnusableReplies(t *testing.T) {
	if _, err := ipConfigFromInfo(Info{IP: "192.0.2.13", Netmask: "255.255.255.0"}); err == nil {
		t.Fatal("a reply without a gateway must be refused")
	}
	got, err := ipConfigFromInfo(Info{IP: "192.0.2.13", Netmask: "255.255.255.0", Gateway: "192.0.2.1"})
	if err != nil || got.String() != "ip 192.0.2.13 mask 255.255.255.0 gateway 192.0.2.1" {
		t.Fatalf("got %v, %v", got, err)
	}
}

// The fake answers from 127.0.0.1, so the "move" keeps the address and changes
// the mask and gateway; the wire layout is what matters here.
func TestSetIPConfigWireLayoutAndReadBack(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.2")); err != nil {
		t.Fatal(err)
	}
	sets := f.AllSets()
	if len(sets) != 1 {
		t.Fatalf("want exactly one write, got %d", len(sets))
	}
	want := []wire.TLV{
		{Type: wire.TLVHostname, Value: []byte("TL-SG108E-fake\x00")}, // kept as the switch reports it
		{Type: wire.TLVDHCP, Value: []byte{0}},                        // off: a switch that leases its address ignores a static one
		{Type: wire.TLVIP, Value: []byte{127, 0, 0, 1}}, {Type: wire.TLVNetmask, Value: []byte{255, 255, 0, 0}}, {Type: wire.TLVGateway, Value: []byte{127, 0, 0, 2}},
	}
	if len(sets[0]) != len(want) {
		t.Fatalf("SET carries %d TLVs, want %d: %+v", len(sets[0]), len(want), sets[0])
	}
	for i, w := range want {
		if sets[0][i].Type != w.Type || !bytes.Equal(sets[0][i].Value, w.Value) {
			t.Errorf("TLV %d = %+v, want %+v", i, sets[0][i], w)
		}
	}
	info, err := c.Info(context.Background())
	if err != nil || info.Netmask != "255.255.0.0" || info.Gateway != "127.0.0.2" {
		t.Fatalf("after the move: %+v err=%v", info, err)
	}
}

func TestSetIPConfigValidatesBeforeTheNetwork(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.0.255.0", "127.0.0.2")); err == nil {
		t.Fatal("an invalid mask must be refused")
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("an invalid request must not write, got %d writes", n)
	}
}

func TestSetIPConfigRefusesUnusableCurrentConfiguration(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.DiscoverReply = []escptest.Setting{{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVMAC, Value: []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}}
	})
	c := dialFake(t, f, "secret")
	err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2"))
	if err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("nothing may be written, got %d writes", n)
	}
}

func TestSetIPConfigErrorsDoNotMoveTheClient(t *testing.T) {
	t.Run("silent switch", func(t *testing.T) {
		f := escptest.Start(t)
		f.Mute()
		c := dialFast(t, f, "secret")
		if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2")); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
	})
	t.Run("wrong credentials", func(t *testing.T) {
		f := escptest.Start(t)
		c := dialFake(t, f, "wrong")
		if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2")); !errors.Is(err, ErrRejected) {
			t.Fatalf("want ErrRejected, got %v", err)
		}
		if !c.dest.IP.Equal(net.ParseIP("127.0.0.1")) || c.alsoFrom != nil {
			t.Fatalf("a rejected write must leave the client where it was: dest=%v alsoFrom=%v", c.dest.IP, c.alsoFrom)
		}
	})
}

func TestSetIPConfigReportsAnUnconfirmedMove(t *testing.T) {
	f := escptest.Start(t)
	c := dialFast(t, f, "secret")
	c.moveWait = 300 * time.Millisecond
	// 127.0.0.2 is where the client looks after the write; the fake stays at 127.0.0.1.
	err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.2", "255.255.255.0", "127.0.0.3"))
	if !errors.Is(err, ErrMoveUnconfirmed) {
		t.Fatalf("want ErrMoveUnconfirmed, got %v", err)
	}
	if !c.dest.IP.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("the client must follow the switch to the new address, dest=%v", c.dest.IP)
	}
}

func TestAcceptsRepliesFromTheNewAddressWhileMoving(t *testing.T) {
	c, err := Dial(Config{Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	data := wire.RC4Static(wire.Marshal(wire.Header{Op: wire.OpSetReply, Seq: c.seq}, nil))
	from := &net.UDPAddr{IP: net.ParseIP("127.0.0.2")}
	if _, ok := c.accept(from, data); ok {
		t.Fatal("a reply from another address must be dropped normally")
	}
	c.alsoFrom = net.ParseIP("127.0.0.2")
	if _, ok := c.accept(from, data); !ok {
		t.Fatal("a reply from the address being moved to must be accepted")
	}
}

func TestSetIPConfigWaitsUntilTheSwitchAnswersAtTheNewAddress(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.StaleAfterSet = 2 }) // two probes still see the old address
	c := dialFake(t, f, "secret")
	start := time.Now()
	if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.2")); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < ipMovePoll {
		t.Fatalf("SetIPConfig returned after %s: it must keep probing until the switch reports the new address", time.Since(start))
	}
	if info, err := c.Info(context.Background()); err != nil || info.Netmask != "255.255.0.0" {
		t.Fatalf("after the move: %+v err=%v", info, err)
	}
}
