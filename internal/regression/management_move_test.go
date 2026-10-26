package regression

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// settleTime bounds each look at the switch while it is being brought back.
const settleTime = 2 * time.Minute

// ipConfigOfInfo is the address triple a discovery reply carries.
func ipConfigOfInfo(i escp.Info) escp.IPConfig {
	return escp.IPConfig{IP: net.ParseIP(i.IP), Netmask: net.ParseIP(i.Netmask), Gateway: net.ParseIP(i.Gateway)}
}

// bringBack puts the switch at its original address whatever happened to the run: when a look at
// the original address shows the switch there, it does nothing; otherwise it talks to the switch
// at the spare address and moves it back. It runs even when the scenario failed halfway.
func (s *Session) bringBack(t *testing.T, original escp.IPConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTime)
	defer cancel()
	if info, err := s.Client.Info(ctx); err == nil && info.IP == original.IP.String() {
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
		return fmt.Sprintf("provider \"tplinkeasysmart\" {\n  host     = trimspace(file(%q))\n  username = %q\n  password = %q\n}\n", hostFile, s.Target.Username, s.Target.Password) +
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
		if err != nil || got.IP != want {
			return fmt.Errorf("the switch at %s reports ip %q, err %w, want %q", host, got.IP, err, want)
		}
		return nil
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: hcl(s.Target.MoveIP, info.Netmask, info.Gateway), Check: func(*terraform.State) error {
			setHost(s.Target.MoveIP) // the next Terraform commands must dial the new address
			return reportsIP(s.Target.MoveIP, s.Target.MoveIP)
		}},
		{Config: hcl(info.IP, info.Netmask, info.Gateway), Check: func(st *terraform.State) error {
			setHost(s.Target.Host)
			if err := reportsIP(s.Target.Host, info.IP); err != nil {
				return err
			}
			return s.restoredCheck(t)(st)
		}},
	}})
}
