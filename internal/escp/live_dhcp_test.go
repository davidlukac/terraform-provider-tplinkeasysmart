//go:build live

package escp

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

// Measures what the switch does when its DHCP flag is written: whether it asks for a lease at once,
// how long that takes, what discovery reports meanwhile and afterwards, whether it still answers at
// the old static address, and what a write of a static address or of the device description does
// while DHCP is on. The answers decide how the provider may manage DHCP (docs/mac-addressing-design.md,
// section 4.5, in the private workspace). Every observation is a line starting with "OBS|"; what is
// asserted is the restore: the switch must end at its static address with DHCP off.
//
// This changes the management address of the switch: it needs TPLINK_EASYSMART_LIVE_DHCP=1, the
// switch's MAC in TPLINK_EASYSMART_MAC (the switch is found by it wherever the lease puts it), a
// router whose DHCP server hands out an address, and a switch that is free to lose its address
// for a minute (switch 03). Take a configuration backup first and watch the pings.
func TestLiveDHCPProbe(t *testing.T) {
	if os.Getenv("TPLINK_EASYSMART_LIVE_DHCP") != "1" {
		t.Skip("set TPLINK_EASYSMART_LIVE_DHCP=1 to switch the DHCP flag of the switch on and back off")
	}
	mac, err := ParseMAC(os.Getenv("TPLINK_EASYSMART_MAC"))
	if err != nil {
		t.Fatalf("TPLINK_EASYSMART_MAC must name the switch: %v", err)
	}
	for _, ro := range strings.Split(os.Getenv("TPLINK_EASYSMART_LIVE_READONLY_MACS"), ",") {
		if strings.EqualFold(strings.TrimSpace(ro), mac.String()) {
			t.Fatalf("refusing: %s is listed in TPLINK_EASYSMART_LIVE_READONLY_MACS", mac)
		}
	}
	user, pass := os.Getenv("TPLINK_EASYSMART_USERNAME"), os.Getenv("TPLINK_EASYSMART_PASSWORD")
	obs := func(format string, args ...any) { t.Logf("OBS|"+format, args...) }
	dial := func(host string) *Client {
		t.Helper()
		c, err := Dial(Config{Host: host, MAC: mac, Username: user, Password: pass, Timeout: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() }) // best effort at the end of the probe
		return c
	}
	ctx := context.Background()
	c := dial(os.Getenv("TPLINK_EASYSMART_HOST"))
	info0, err := c.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info0.DHCP {
		t.Fatalf("refusing to start: DHCP is already on (%+v)", info0)
	}
	static, err := ipConfigFromInfo(info0)
	if err != nil {
		t.Fatal(err)
	}
	obs("start: %s hostname %q dhcp=%t mac %s", static, info0.Hostname, info0.DHCP, info0.MAC)
	started := time.Now()
	since := func() string { return time.Since(started).Round(100 * time.Millisecond).String() }
	setSystem := func(cl *Client, dhcp bool, cfg IPConfig) {
		sent, err := cl.writeSystem(ctx, Operation{Name: "SetIPConfig", Impact: ImpactManagement}, info0.Hostname, dhcp, cfg)
		obs("%s write dhcp=%t %s -> sent=%t err=%v", since(), dhcp, cfg, sent, err)
	}
	// look is one discovery by MAC.
	look := func() (Info, error) {
		lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return Locate(lctx, mac, DiscoverOptions{})
	}
	// watch logs every change of what discovery reports, for up to limit, and returns the last report.
	watch := func(limit time.Duration, done func(Info) bool) Info {
		var last Info
		var lastLine string
		for end := time.Now().Add(limit); time.Now().Before(end); time.Sleep(time.Second) {
			got, err := look()
			line := fmt.Sprintf("ip %s mask %s gateway %s dhcp=%t", got.IP, got.Netmask, got.Gateway, got.DHCP)
			if err != nil {
				line = "nobody answers"
			}
			if line != lastLine {
				obs("%s discovery: %s", since(), line)
				lastLine = line
			}
			if err == nil {
				last = got
				if done != nil && done(got) {
					return got
				}
			}
		}
		return last
	}

	// The restore runs whatever happened: find the switch wherever it is, write DHCP off with the
	// static address it had, and wait until discovery shows it there.
	defer func() {
		now, err := look()
		if err != nil {
			t.Errorf("RESTORE FAILED: nobody with MAC %s answers a discovery: %v", mac, err)
			return
		}
		if now.IP == static.IP && !now.DHCP {
			obs("restore: the switch is already at %s with DHCP off", now.IP)
			return
		}
		setSystem(dial(now.IP.String()), false, static)
		end := watch(60*time.Second, func(i Info) bool { return i.IP == static.IP && !i.DHCP })
		if end.IP != static.IP || end.DHCP {
			t.Errorf("RESTORE FAILED: the switch reports %+v, want %s with DHCP off", end, static.IP)
		}
	}()

	// 1. DHCP on, keeping the static address in the request as the vendor utility does.
	setSystem(c, true, static)
	leased := watch(90*time.Second, func(i Info) bool { return i.DHCP && i.IP != static.IP })
	obs("%s after DHCP on: ip %s mask %s gateway %s dhcp=%t", since(), leased.IP, leased.Netmask, leased.Gateway, leased.DHCP)

	// 2. Is it still reachable and manageable at the lease? A client that has only the MAC finds it.
	byMAC, err := Dial(Config{MAC: mac, Username: user, Password: pass, Timeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = byMAC.Close() }() // best effort at the end of the probe
	if led, err := byMAC.LED(ctx); err != nil {
		obs("%s management at the lease: LED read failed: %v", since(), err)
	} else {
		obs("%s management at the lease: LED read ok (%t) at %s", since(), led, byMAC.dest.IP)
	}

	// 3. A static address written while DHCP is on: is it ignored (the vendor notes say so)?
	if leased.DHCP {
		other := IPConfig{IP: netip.MustParseAddr("192.168.77.14"), Netmask: static.Netmask, Gateway: static.Gateway}
		setSystem(byMAC, true, other)
		after := watch(15*time.Second, nil)
		obs("%s after a static address was written with DHCP on: ip %s dhcp=%t (was %s)", since(), after.IP, after.DHCP, leased.IP)

		// 4. The device description written while DHCP is on: the request carries the address as
		// read and the flag as read, which is what the provider's system resource does.
		err := byMAC.SetHostname(ctx, info0.Hostname)
		after = watch(10*time.Second, nil)
		obs("%s after SetHostname with DHCP on: err=%v ip %s dhcp=%t", since(), err, after.IP, after.DHCP)
	}
}
