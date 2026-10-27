package regression

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// lookupAfterMove bounds how long a check keeps looking for a switch by MAC after it moved.
const lookupAfterMove = 30 * time.Second

// settleTime bounds each look at the switch while it is being brought back.
const settleTime = 2 * time.Minute

// ipConfigOfInfo is the address triple a discovery reply carries.
func ipConfigOfInfo(i escp.Info) escp.IPConfig {
	return escp.IPConfig{IP: i.IP, Netmask: i.Netmask, Gateway: i.Gateway}
}

// bringBack puts the switch at its original address whatever happened to the run: when a look at
// the original address shows the switch there, it does nothing; otherwise it talks to the switch
// at the spare address and moves it back. It runs even when the scenario failed halfway.
func (s *Session) bringBack(t *testing.T, original escp.IPConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTime)
	defer cancel()
	if info, err := s.Client.Info(ctx); err == nil && info.IP.String() == original.IP.String() {
		return
	}
	t.Logf("the switch is not at %s; moving it back from %s", original.IP, s.Target.MoveIP)
	c, err := s.dialClient(escp.Config{Host: s.Target.MoveIP, Username: s.Target.Username, Password: s.Target.Password})
	if err != nil {
		t.Errorf("CANNOT REACH THE SWITCH: dialling %s: %v. Look for it at %s and at %s (discovery) before doing anything else", s.Target.MoveIP, err, original.IP, s.Target.MoveIP)
		return
	}
	defer func() { _ = c.Close() }() // best effort at the end of the run
	if err := c.SetIPConfig(ctx, original); err != nil {
		t.Errorf("CANNOT MOVE THE SWITCH BACK: %v. It may be at %s or at %s; look at both before doing anything else", err, original.IP, s.Target.MoveIP)
	}
}

// Moving the management address through the provider: switch 03 goes to the spare address and
// back, only when the run opted in (TPLINK_REGRESSION_ALLOW_MANAGEMENT_MOVE=1 and a spare address
// that nothing uses; it is not probed before the move). The provider block reads its host from a
// file the checks rewrite, because the provider process of the next Terraform command must dial
// the address the switch has by then. The fake cannot do this: it answers on loopback, where the
// client refuses to move a switch.
func TestScenarioManagementMove(t *testing.T) {
	s := openForScenario(t)
	if s.Fake != nil {
		t.Skip("the move needs hardware: the fake answers on loopback, where the client refuses to move a switch")
	}
	if s.Target.MoveIP == "" {
		t.Skip("set " + EnvAllowMove + "=1 and TPLINK_REGRESSION_MOVE_IP to move the switch to a spare address and back")
	}
	ctx := context.Background()
	info, err := s.Client.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.DHCP {
		t.Fatal("the switch uses DHCP; the management address resource only manages a static address")
	}
	original := ipConfigOfInfo(info)
	t.Cleanup(func() { s.bringBack(t, original) }) // registered before the first write

	hostFile := filepath.Join(t.TempDir(), "host")
	setHost := func(h string) {
		if err := os.WriteFile(hostFile, []byte(h), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setHost(s.Target.Host)
	hcl := func(ip, mask, gateway string) string {
		return fmt.Sprintf("provider \"tplinkeasysmart\" {\n  host     = trimspace(file(%q))\n  username = %q\n  password = %q\n}\n", hostFile, s.Target.Username, s.Target.Password.Reveal()) +
			fmt.Sprintf("resource \"tplinkeasysmart_management_ip\" \"this\" {\n  ip_address       = %q\n  netmask          = %q\n  gateway          = %q\n  allow_disruptive = true\n}\n", ip, mask, gateway)
	}
	// reportsIP checks, with a client of its own at the address the switch is expected to have, what discovery says.
	reportsIP := func(host, want string) error {
		c, err := s.dialClient(escp.Config{Host: host, Username: s.Target.Username, Password: s.Target.Password})
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }() // read-only client at the end of the check
		got, err := c.Info(ctx)
		if err != nil || got.IP.String() != want {
			return fmt.Errorf("the switch at %s reports ip %q, err %w, want %q", host, got.IP.String(), err, want)
		}
		return nil
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: hcl(s.Target.MoveIP, info.Netmask.String(), info.Gateway.String()), Check: func(*terraform.State) error {
			setHost(s.Target.MoveIP) // the next Terraform commands must dial the new address
			return reportsIP(s.Target.MoveIP, s.Target.MoveIP)
		}},
		{Config: hcl(info.IP.String(), info.Netmask.String(), info.Gateway.String()), Check: func(st *terraform.State) error {
			setHost(s.Target.Host)
			if err := reportsIP(s.Target.Host, info.IP.String()); err != nil {
				return err
			}
			return s.restoredCheck(t)(st)
		}},
	}})
}

// The same move with the switch named by its MAC address: no host file, no override. The next
// Terraform command finds the switch at the spare address by itself, which is the end-to-end
// proof that a changed management address no longer needs a configuration change.
func TestScenarioManagementMoveFoundByMAC(t *testing.T) {
	s := openForScenario(t)
	if s.Fake != nil {
		t.Skip("the move needs hardware: the fake answers on loopback, where the client refuses to move a switch")
	}
	if s.Target.MoveIP == "" {
		t.Skip("set " + EnvAllowMove + "=1 and TPLINK_REGRESSION_MOVE_IP to move the switch to a spare address and back")
	}
	ctx := context.Background()
	info, err := s.Client.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.DHCP {
		t.Fatal("the switch uses DHCP; the management address resource only manages a static address")
	}
	hw := info.MAC
	original := ipConfigOfInfo(info)
	t.Cleanup(func() { s.bringBack(t, original) }) // registered before the first write

	hcl := func(ip, mask, gateway string) string {
		return s.ProviderHCLByMAC("", info.MAC.String()) +
			fmt.Sprintf("resource \"tplinkeasysmart_management_ip\" \"this\" {\n  ip_address       = %q\n  netmask          = %q\n  gateway          = %q\n  allow_disruptive = true\n}\n", ip, mask, gateway)
	}
	// reportsIP asks, with a client that has only the MAC, where the switch is now. The switch
	// takes a moment to answer a broadcast discovery at its new address, so it looks again for a
	// while and says in the log how long that took: that delay is what a command run right after a
	// move has to live with.
	reportsIP := func(want string) error {
		started := time.Now()
		var last error
		for attempt := 1; time.Since(started) < lookupAfterMove; attempt++ {
			c, err := s.dialClient(escp.Config{MAC: hw, Username: s.Target.Username, Password: s.Target.Password})
			if err != nil {
				return err
			}
			got, err := c.Info(ctx)
			_ = c.Close() // read-only client at the end of the check
			if err == nil && got.IP.String() == want {
				t.Logf("found by MAC at %s after %d attempt(s), %s after the move", want, attempt, time.Since(started).Round(time.Millisecond))
				return nil
			}
			last = fmt.Errorf("the switch found by MAC reports ip %q, err %w, want %q", got.IP.String(), err, want)
		}
		return last
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: hcl(s.Target.MoveIP, info.Netmask.String(), info.Gateway.String()), Check: func(*terraform.State) error { return reportsIP(s.Target.MoveIP) }},
		{Config: hcl(info.IP.String(), info.Netmask.String(), info.Gateway.String()), Check: func(st *terraform.State) error {
			if err := reportsIP(info.IP.String()); err != nil {
				return err
			}
			return s.restoredCheck(t)(st)
		}},
	}})
}

// bringBackFromALease puts the switch back on its static address whatever happened to the run: when
// the switch is already there with DHCP off it does nothing; otherwise it looks the switch up by its
// MAC address, wherever the lease put it, and writes the static address back. It runs even when the
// scenario failed halfway.
func (s *Session) bringBackFromALease(t *testing.T, mac escp.MAC, original escp.IPConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTime)
	defer cancel()
	found, err := escp.Locate(ctx, mac, escp.DiscoverOptions{})
	if err != nil {
		t.Errorf("CANNOT FIND THE SWITCH: %v. Look for MAC %s with a discovery before doing anything else", err, mac)
		return
	}
	if found.IP.String() == original.IP.String() && !found.DHCP {
		return
	}
	t.Logf("the switch is at %s with DHCP %t; writing %s back", found.IP.String(), found.DHCP, original)
	c, err := s.dialClient(escp.Config{Host: found.IP.String(), MAC: mac, Username: s.Target.Username, Password: s.Target.Password})
	if err != nil {
		t.Errorf("CANNOT REACH THE SWITCH at %s: %v", found.IP.String(), err)
		return
	}
	defer func() { _ = c.Close() }() // best effort at the end of the run
	if err := c.SetIPConfig(ctx, original); err != nil {
		t.Errorf("CANNOT PUT THE STATIC ADDRESS BACK: %v. The switch may be at %s or at %s; look for MAC %s before doing anything else", err, found.IP.String(), original.IP, mac)
	}
}

// Turning DHCP on and off through the provider, with the switch named by its MAC address only: the
// router's DHCP server gives it an address, the next Terraform commands find it there by themselves,
// and the static address goes back with the DHCP flag off. Only when the run opted in
// (TPLINK_REGRESSION_ALLOW_DHCP=1) and on hardware: the fake has no network to lease from.
func TestScenarioManagementDHCP(t *testing.T) {
	s := openForScenario(t)
	if s.Fake != nil {
		t.Skip("the lease needs hardware and a DHCP server; the provider and library tests cover it on the fake")
	}
	if !s.Target.DHCP {
		t.Skip("set " + EnvAllowDHCP + "=1 to turn DHCP on and back off (the router's DHCP server must be up)")
	}
	ctx := context.Background()
	info, err := s.Client.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.DHCP {
		t.Fatal("the switch already leases its address; the scenario starts from a static one")
	}
	hw := info.MAC
	original := ipConfigOfInfo(info)
	t.Cleanup(func() { s.bringBackFromALease(t, hw, original) }) // registered before the first write

	provider := s.ProviderHCLByMAC("", info.MAC.String())
	dhcp := provider + "resource \"tplinkeasysmart_management_ip\" \"this\" {\n  dhcp             = true\n  allow_disruptive = true\n}\n"
	static := provider + fmt.Sprintf("resource \"tplinkeasysmart_management_ip\" \"this\" {\n  ip_address       = %q\n  netmask          = %q\n  gateway          = %q\n  allow_disruptive = true\n}\n", info.IP.String(), info.Netmask.String(), info.Gateway.String())
	leased := func(*terraform.State) error {
		found, err := escp.Locate(ctx, hw, escp.DiscoverOptions{})
		if err != nil || !found.DHCP {
			return fmt.Errorf("the switch found by MAC reports %+v, err %w, want DHCP on", found, err)
		}
		t.Logf("leased address %s mask %s gateway %s", found.IP.String(), found.Netmask.String(), found.Gateway.String())
		return nil
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: dhcp, Check: resource.ComposeAggregateTestCheckFunc(leased, resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.this", "dhcp", "true"),
			resource.TestCheckResourceAttrSet("tplinkeasysmart_management_ip.this", "ip_address"))},
		{Config: dhcp}, // the second look: nothing to change, the plan after the apply is empty
		{Config: static, Check: func(st *terraform.State) error {
			found, err := escp.Locate(ctx, hw, escp.DiscoverOptions{})
			if err != nil || found.DHCP || found.IP.String() != info.IP.String() {
				return fmt.Errorf("the switch found by MAC reports %+v, err %w, want %s with DHCP off", found, err, info.IP.String())
			}
			return s.restoredCheck(t)(st)
		}},
	}})
}
