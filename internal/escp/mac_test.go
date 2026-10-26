package escp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// The identity of a switch: a client configured with a MAC address never talks to a device with
// another one, finds the switch by its MAC when it has no usable address, and Locate looks one up.

var (
	wantMAC  = net.HardwareAddr(escptest.DefaultMAC[:])
	otherMAC = [6]byte{0x02, 0, 0, 0, 0, 0x77}
)

// dialMAC is a client for the fake that pins the identity to mac. host is "" for a client that
// has to find the switch itself; locator stands in for the discovery probe and counts its calls.
func dialMAC(t *testing.T, f *escptest.Switch, host string, mac net.HardwareAddr, locator func(net.HardwareAddr) (Info, error)) (*Client, *int) {
	t.Helper()
	c := newBoundClient(t, Config{Host: host, MAC: mac, Username: "admin", Password: "secret", Timeout: 150 * time.Millisecond, Retries: NoRetries,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1"})
	c.allowLoopback = true
	calls := new(int)
	c.locator = func(_ context.Context, m net.HardwareAddr) (Info, error) {
		*calls++
		if locator == nil {
			return Info{IP: "127.0.0.1", MAC: m.String()}, nil
		}
		return locator(m)
	}
	t.Cleanup(func() { c.Close() })
	return c, calls
}

func TestDialRefusesAMACOfTheWrongLength(t *testing.T) {
	if _, err := Dial(Config{Host: "127.0.0.1", MAC: net.HardwareAddr{1, 2, 3}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a 3-byte MAC must be ErrInvalid, got %v", err)
	}
}

func TestAClientWithoutAnAddressSendsNothingUntilItIsUsedAndThenFindsTheSwitch(t *testing.T) {
	f := escptest.Start(t)
	c, calls := dialMAC(t, f, "", wantMAC, nil)
	if f.Received() != 0 || *calls != 0 {
		t.Fatalf("Dial must not touch the network: %d packets, %d lookups", f.Received(), *calls)
	}
	if info, err := c.Info(context.Background()); err != nil || info.MAC != wantMAC.String() {
		t.Fatalf("Info = %+v, %v", info, err)
	}
	if _, err := c.Info(context.Background()); err != nil || *calls != 1 {
		t.Fatalf("the address is kept: %d lookups, err %v", *calls, err)
	}
}

func TestAClientWithoutAnAddressReportsAFailedLookup(t *testing.T) {
	f := escptest.Start(t)
	notFound := func(m net.HardwareAddr) (Info, error) { return Info{}, ErrNotFound }
	c, _ := dialMAC(t, f, "", wantMAC, notFound)
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	noAddress := func(m net.HardwareAddr) (Info, error) { return Info{IP: "not an address"}, nil }
	c, _ = dialMAC(t, f, "", wantMAC, noAddress)
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an answer without a usable address must be ErrNotFound, got %v", err)
	}
}

func TestADeviceWithAnotherMACIsNeverWrittenTo(t *testing.T) {
	f := escptest.Start(t)
	f.SetMAC(otherMAC)
	c, _ := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	var wrong *WrongSwitchError
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrWrongSwitch) || !errors.As(err, &wrong) || wrong.Got.String() != net.HardwareAddr(otherMAC[:]).String() {
		t.Fatalf("Info = %v, want ErrWrongSwitch naming %x", err, otherMAC)
	}
	if err := c.SetLED(context.Background(), false); !errors.Is(err, ErrWrongSwitch) {
		t.Fatalf("SetLED = %v, want ErrWrongSwitch", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("nothing may be written to the wrong device, got %d writes", n)
	}
	if got := wrong.Error(); got == "" || wrong.IP == nil {
		t.Fatalf("the error names both addresses and where the device is: %q %v", got, wrong.IP)
	}
}

func TestTheMACInTheDiscoveryReplyIsCheckedToo(t *testing.T) {
	// the header carries the wanted MAC but the description names another device
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.DiscoverReply = []escptest.Setting{{Type: wire.TLVMAC, Value: otherMAC[:]}, {Type: wire.TLVIP, Value: []byte{127, 0, 0, 1}}}
	})
	c, _ := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrWrongSwitch) {
		t.Fatalf("got %v, want ErrWrongSwitch", err)
	}
}

func TestTheDeviceChangingBehindTheClientIsCaught(t *testing.T) {
	f := escptest.Start(t)
	c, _ := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	if _, err := c.LED(context.Background()); err != nil {
		t.Fatal(err)
	}
	// the address now belongs to another device, which has no session with us and so ignores the
	// request; the client starts a new session, and the reply to the key exchange names the stranger
	f.SetMAC(otherMAC)
	f.ClearSession()
	if err := c.SetLED(context.Background(), false); !errors.Is(err, ErrWrongSwitch) {
		t.Fatalf("got %v, want ErrWrongSwitch", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("the write must not reach the other device, got %d", n)
	}
}

func TestAnAddressThatStopsAnsweringIsLookedUpAgainByMAC(t *testing.T) {
	f := escptest.Start(t)
	f.DropFirst(2) // the first discovery (to the address and to the broadcast address) goes unanswered
	c, calls := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	if _, err := c.Info(context.Background()); err != nil || *calls != 1 {
		t.Fatalf("Info = %v after %d lookups, want success after one", err, *calls)
	}
	f2 := escptest.Start(t)
	f2.DropFirst(2)
	c, _ = dialMAC(t, f2, "127.0.0.1", wantMAC, func(net.HardwareAddr) (Info, error) { return Info{}, ErrNotFound })
	if _, err := c.Info(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want the failed lookup", err)
	}
}

func TestASessionThatDiesIsRetriedAtTheAddressFoundByMAC(t *testing.T) {
	f := escptest.Start(t)
	c, calls := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	if err := c.SetLED(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	f.ClearSession() // the switch forgot the session: the next request gets no reply
	if _, err := c.LED(context.Background()); err != nil || *calls == 0 {
		t.Fatalf("LED = %v after %d lookups, want success after a lookup", err, *calls)
	}
}

func TestLocateStopsAtTheFirstReplyFromTheWantedSwitch(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	macA, macB := [6]byte{0xa8, 0x42, 0xa1, 0, 0, 0x30}, [6]byte{0xc0, 0x3a, 0x55, 0, 0, 0x09}
	r := startResponder(t, hostPort, func(h wire.Header) [][]byte {
		return [][]byte{
			packet(wire.OpReply, h.Seq, discoverTLVs(macA, [4]byte{192, 0, 2, 30}, "sw-a")),
			packet(wire.OpReply, h.Seq, discoverTLVs(macB, [4]byte{192, 0, 2, 9}, "sw-b")),
		}
	})
	env := loopbackEnv(t, r, hostPort, netInterface{Name: "en0", MAC: macOne, Broadcasts: lo})
	start := time.Now()
	info, err := locateWith(context.Background(), net.HardwareAddr(macB[:]), DiscoverOptions{Window: time.Minute}, env)
	if err != nil || info.IP != "192.0.2.9" || info.Hostname != "sw-b" {
		t.Fatalf("got %+v, %v", info, err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("Locate waited for the window instead of stopping at the reply (%v)", time.Since(start))
	}
}

func TestLocateReportsASwitchThatDoesNotAnswer(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, func(h wire.Header) [][]byte {
		return [][]byte{packet(wire.OpReply, h.Seq, discoverTLVs(macTwo, [4]byte{192, 0, 2, 9}, "other"))}
	})
	env := loopbackEnv(t, r, hostPort, netInterface{Name: "en0", MAC: macOne, Broadcasts: lo})
	if _, err := locateWith(context.Background(), net.HardwareAddr(macOne[:]), DiscoverOptions{Window: 200 * time.Millisecond}, env); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if _, err := locateWith(context.Background(), net.HardwareAddr{1, 2}, DiscoverOptions{}, env); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a short MAC must be ErrInvalid, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locateWith(ctx, net.HardwareAddr(macOne[:]), DiscoverOptions{Window: time.Minute}, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled lookup must say so, got %v", err)
	}
}

func TestLocateOnAnInterfaceThisHostDoesNotHaveSendsNothing(t *testing.T) {
	if _, err := Locate(context.Background(), wantMAC, DiscoverOptions{Interface: "no-such-interface-escp"}); !errors.Is(err, ErrNoInterface) {
		t.Fatalf("got %v, want ErrNoInterface", err)
	}
}

// A dead address must not cost the normal timeout of every attempt before the lookup by MAC
// starts: the first contact gets a short budget (a real switch answers a discovery in under half
// a second; measured: a dead host with the normal timeouts took 9.6 s to fall back).
func TestADeadAddressGetsAShortBudgetBeforeTheLookup(t *testing.T) {
	f := escptest.Start(t)
	f.DropFirst(2)
	c, calls := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	c.cfg.Timeout = time.Minute // the budget, not the per-attempt timeout, ends the wait
	start := time.Now()
	if _, err := c.Info(context.Background()); err != nil || *calls != 1 {
		t.Fatalf("Info = %v after %d lookups", err, *calls)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("the dead address was waited on for %v", took)
	}
	// a caller that cancels is not retried through the lookup
	f2 := escptest.Start(t)
	f2.DropFirst(2)
	c, calls = dialMAC(t, f2, "127.0.0.1", wantMAC, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Info(ctx); !errors.Is(err, context.Canceled) || *calls != 0 {
		t.Fatalf("a cancelled call must not look the switch up: %v after %d lookups", err, *calls)
	}
}
