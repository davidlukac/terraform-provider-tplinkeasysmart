package escp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// dialLeasing is a client for the fake that pins its MAC and looks the switch up by asking the fake
// itself (the fake cannot be found by a broadcast on this host). The returned counter is the number
// of lookups.
func dialLeasing(t *testing.T, f *escptest.Switch) (*Client, *int) {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", MAC: MAC(escptest.DefaultMAC), Username: "admin", Password: "secret",
		Timeout: 150 * time.Millisecond, Retries: 1, SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1"})
	c.allowLoopback = true
	lookups := new(int)
	c.locator = func(ctx context.Context, _ MAC) (Info, error) { *lookups++; return c.discoverOnce(ctx) }
	t.Cleanup(func() { c.Close() })
	return c, lookups
}

func TestEnableDHCPAsksForALeaseAndFollowsIt(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialLeasing(t, f)
	if err := c.EnableDHCP(context.Background()); err != nil {
		t.Fatal(err)
	}
	sets := f.AllSets()
	if len(sets) != 1 {
		t.Fatalf("want exactly one write, got %d", len(sets))
	}
	// The request carries the description and the current static address as read, and the flag on.
	want := []wire.TLV{
		{Type: wire.TLVHostname, Value: []byte("TL-SG108E-fake\x00")}, {Type: wire.TLVDHCP, Value: []byte{1}},
		{Type: wire.TLVIP, Value: []byte{127, 0, 0, 1}}, {Type: wire.TLVNetmask, Value: []byte{255, 255, 255, 0}}, {Type: wire.TLVGateway, Value: []byte{127, 0, 0, 254}},
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
	if err != nil || !info.DHCP || info.Netmask.String() != "255.255.0.0" || info.Gateway.String() != "127.0.0.2" {
		t.Fatalf("after the lease: %+v err=%v", info, err)
	}
	if !c.dest.IP.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("the client must be at the leased address, is at %v", c.dest.IP)
	}
}

func TestEnableDHCPWaitsOutTheSilenceWhileTheSwitchAsksForALease(t *testing.T) {
	f := escptest.Start(t)
	f.SetLease(escptest.Lease{IP: escptest.DefaultLease.IP, Mask: escptest.DefaultLease.Mask, Gateway: escptest.DefaultLease.Gateway, Silence: 6})
	c, lookups := dialLeasing(t, f)
	if err := c.EnableDHCP(context.Background()); err != nil {
		t.Fatal(err)
	}
	if *lookups < 2 {
		t.Fatalf("the switch was silent, so the lookup must have been repeated; %d lookup(s)", *lookups)
	}
}

func TestEnableDHCPNeedsTheMACAndSendsNothingWithout(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.EnableDHCP(context.Background()); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "Config.MAC") {
		t.Fatalf("got %v, want ErrInvalid naming Config.MAC", err)
	}
	if f.Received() != 0 {
		t.Fatalf("%d packet(s) reached the switch", f.Received())
	}
}

func TestEnableDHCPOnASwitchThatLeasesWritesNothing(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialLeasing(t, f)
	for i := 0; i < 2; i++ {
		if err := c.EnableDHCP(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("the second call must not write: %d writes", n)
	}
}

// The reply to the write was lost but the switch took it: the lookup shows the lease.
func TestEnableDHCPSucceedsWhenTheReplyIsLostButTheSwitchLeases(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.OnSystemSet = escptest.SystemSetLeaves })
	c, _ := dialLeasing(t, f)
	if err := c.EnableDHCP(context.Background()); err != nil {
		t.Fatalf("the switch leases, so the call must succeed: %v", err)
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("the write must be sent once and not repeated, got %d", n)
	}
}

func TestEnableDHCPReportsAWriteThatWasNotApplied(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.DropSets = true })
	c, _ := dialLeasing(t, f)
	c.moveWait = 400 * time.Millisecond
	err := c.EnableDHCP(context.Background())
	if !errors.Is(err, ErrNoReply) || errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "not switched on") {
		t.Fatalf("want ErrNoReply saying DHCP was not switched on, got %v", err)
	}
	if f.SetsDropped() == 0 {
		t.Fatal("the write never left the client, so this proves nothing")
	}
}

func TestEnableDHCPReportsASwitchThatAnswersButDoesNotLease(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialLeasing(t, f)
	c.moveWait = 400 * time.Millisecond
	c.locator = func(ctx context.Context, _ MAC) (Info, error) {
		return Info{MAC: c.cfg.MAC, IP: netip.MustParseAddr("127.0.0.1"), Netmask: netip.MustParseAddr("255.255.255.0"), Gateway: netip.MustParseAddr("127.0.0.254")}, nil
	}
	err := c.EnableDHCP(context.Background())
	if !errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "DHCP off") {
		t.Fatalf("want ErrMoveUnconfirmed saying DHCP is off, got %v", err)
	}
}

func TestEnableDHCPReportsASwitchThatCannotBeFound(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.OnSystemSet = escptest.SystemSetVanishes })
	c, _ := dialLeasing(t, f)
	c.moveWait = 400 * time.Millisecond
	err := c.EnableDHCP(context.Background())
	if !errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "nobody with the configured MAC answers") {
		t.Fatalf("want ErrMoveUnconfirmed saying nobody answers, got %v", err)
	}
}

func TestEnableDHCPDoesNotWaitAfterARefusal(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom = 1 })
	c, lookups := dialLeasing(t, f)
	var rejected *RejectedError
	if err := c.EnableDHCP(context.Background()); !errors.As(err, &rejected) {
		t.Fatalf("want the switch's refusal, got %v", err)
	}
	if *lookups != 0 {
		t.Fatalf("a refused write was not applied, so no lookup is needed: %d", *lookups)
	}
}

// A caller that gives up after the write was sent still gets the answer: the lookup runs on its own budget.
func TestEnableDHCPSettlesWhenTheCallIsCancelledAfterTheWrite(t *testing.T) {
	f := escptest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	f.Configure(func(f *escptest.Switch) {
		f.OnSystemSet = escptest.SystemSetLeaves
		f.AfterSystemSet = cancel
	})
	c, _ := dialLeasing(t, f)
	if err := c.EnableDHCP(ctx); err != nil {
		t.Fatalf("the switch leases, so the call must succeed although the caller gave up: %v", err)
	}
}

// SetIPConfig is the way back: it writes the flag off with the address, and the switch is static there.
func TestSetIPConfigTurnsDHCPOff(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialLeasing(t, f)
	if err := c.EnableDHCP(context.Background()); err != nil {
		t.Fatal(err)
	}
	static := cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.254")
	if err := c.SetIPConfig(context.Background(), static); err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(context.Background())
	if err != nil || info.DHCP || info.Netmask.String() != "255.255.255.0" || info.Gateway.String() != "127.0.0.254" {
		t.Fatalf("after DHCP off: %+v err=%v", info, err)
	}
	last := f.LastSet()
	if len(last) < 2 || last[1].Type != wire.TLVDHCP || last[1].Value[0] != 0 {
		t.Fatalf("the write must carry the flag off: %+v", last)
	}
}

// Nothing is written when the switch cannot be read first, or when what it reports about its own
// address is unusable: the request carries that address back.
func TestEnableDHCPWritesNothingWhenTheSwitchCannotBeReadFirst(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialLeasing(t, f)
	f.Mute()
	if err := c.EnableDHCP(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("got %v, want ErrNoReply", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("%d writes reached a switch that was never read", n)
	}

	f = escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.DiscoverReply = []escptest.Setting{{Type: wire.TLVMAC, Value: escptest.DefaultMAC[:]}} // no addresses
	})
	c, _ = dialLeasing(t, f)
	if err := c.EnableDHCP(context.Background()); err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
		t.Fatalf("got %v, want a refusal to write an unusable address back", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("%d writes with an unusable address", n)
	}
}

// A reply that says DHCP is on but carries no usable address is not a lease yet.
func TestEnableDHCPKeepsWaitingForAUsableLease(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialLeasing(t, f)
	c.moveWait = 400 * time.Millisecond
	c.locator = func(context.Context, MAC) (Info, error) {
		return Info{MAC: c.cfg.MAC, DHCP: true}, nil
	}
	err := c.EnableDHCP(context.Background())
	if !errors.Is(err, ErrMoveUnconfirmed) || !strings.Contains(err.Error(), "DHCP on") {
		t.Fatalf("want ErrMoveUnconfirmed saying DHCP is on without an address, got %v", err)
	}
}

// A caller that has already given up sends nothing: not the read before the write, and not the write.
func TestEnableDHCPWithACancelledContextSendsNothing(t *testing.T) {
	f := escptest.Start(t)
	c, lookups := dialLeasing(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.EnableDHCP(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
	if f.WriteCount() != 0 || *lookups != 0 {
		t.Fatalf("%d write(s) and %d lookup(s) after the caller gave up", f.WriteCount(), *lookups)
	}
}
