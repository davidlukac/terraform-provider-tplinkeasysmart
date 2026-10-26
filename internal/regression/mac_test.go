package regression

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// Naming a switch by its MAC address. On hardware the provider really finds the switch with a
// discovery on this host's network; on the fake a stand-in for that discovery (Session.dialClient)
// finds the fake, so the same scenarios prove that the provider passes the MAC on, that the
// identity pin refuses another device, and that nothing is written when it does.

// notTheTarget is a MAC address (locally administered, so no vendor owns it) that no switch has.
const notTheTarget = "02:00:00:00:00:01"

// ledScenarioByMAC flips the LED through a provider block that names the switch as given, and puts
// it back: the smallest write that proves the whole path works.
func (s *Session) ledScenarioByMAC(t *testing.T, providerHCL string) {
	t.Helper()
	orig, err := s.Client.LED(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(on bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_led\" \"this\" {\n  enabled = %t\n}\n", on)
	}
	s.scenarioWithProvider(t, providerHCL, cfg(!orig), func(*terraform.State) error {
		got, err := s.Client.LED(context.Background())
		if err != nil || got != !orig {
			return fmt.Errorf("LED on the switch = %t, err %w, want %t", got, err, !orig)
		}
		return nil
	}, cfg(orig))
}

// A provider block with a MAC address and no host manages the switch end to end.
func TestScenarioSwitchNamedByMACAlone(t *testing.T) {
	s := openForScenario(t)
	s.ledScenarioByMAC(t, s.ProviderHCLByMAC("", s.MAC))
}

// With a host as well, the MAC is the identity pin and the host is where to look first.
func TestScenarioSwitchNamedByHostAndMAC(t *testing.T) {
	s := openForScenario(t)
	s.ledScenarioByMAC(t, s.ProviderHCLByMAC(s.Target.Host, s.MAC))
}

// A host that answers as another device is never written to: the pin refuses it before any write.
// The switch at the host is the target; the configuration claims it is another one.
func TestScenarioAnotherDeviceAtTheHostIsNeverWrittenTo(t *testing.T) {
	s := openForScenario(t)
	writes := 0
	if s.Fake != nil {
		writes = s.Fake.WriteCount()
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{{
		Config: s.ProviderHCLByMAC(s.Target.Host, notTheTarget) + "resource \"tplinkeasysmart_led\" \"this\" {\n  enabled = false\n}\n",
		// (?s): Terraform wraps the diagnostic at its own line width.
		ExpectError: regexp.MustCompile(`(?s)not\s+the\s+configured\s+switch.*configuration\s+names\s+` + notTheTarget),
	}}})
	if s.Fake != nil && s.Fake.WriteCount() != writes {
		t.Fatalf("the refused configuration wrote to the switch: write count %d, was %d", s.Fake.WriteCount(), writes)
	}
	if diff, err := s.Restored(context.Background()); err != nil || len(diff) > 0 {
		t.Fatalf("the switch is not as it was: %q, err %v", diff, err)
	}
}

// A MAC that nobody has is a clear failure, not a hang and not a write.
func TestScenarioAMACNobodyHasIsNotFound(t *testing.T) {
	s := openForScenario(t)
	if s.Fake == nil {
		t.Skip("on hardware a lookup that finds nobody takes the whole discovery window; the library test covers it")
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{{
		Config:      s.ProviderHCLByMAC("", notTheTarget) + "resource \"tplinkeasysmart_led\" \"this\" {\n  enabled = false\n}\n",
		ExpectError: regexp.MustCompile(`(?s)no\s+switch\s+with\s+that\s+MAC\s+address\s+answered`),
	}}})
}

// The case the feature exists for: the configured address is stale (the lease changed) and the
// switch is found by its MAC. Hardware only: the fake has no network to search.
func TestScenarioAStaleHostIsReplacedByTheLookup(t *testing.T) {
	s := openForScenario(t)
	if s.Fake != nil {
		t.Skip("needs a network to search; the library tests cover the fallback on the fake")
	}
	// 192.0.2.0/24 is reserved for documentation: nothing answers there.
	s.ledScenarioByMAC(t, s.ProviderHCLByMAC("192.0.2.1", s.MAC))
}

// Two switches named by MAC alone: each resource lands on its own switch. Fakes only.
func TestScenarioMultiSwitchByMAC(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("scenarios need TF_ACC=1 (make regression)")
	}
	if target, err := LoadTarget(map[string]string{EnvTarget: os.Getenv(EnvTarget)}); err != nil || target.Real() {
		t.Skip("the multi-switch scenario runs on the fakes only")
	}
	macA, macB := escp.MAC(escptest.DefaultMAC), escp.MAC{0x00, 0x00, 0x5e, 0x00, 0x53, 0x03}
	a, b := startFake(t), startFake(t)
	b.SetMAC(macB)
	fakes := map[string]*escptest.Switch{macA.String(): a, macB.String(): b}
	dial := func(cfg escp.Config) (provider.SwitchClient, error) {
		f, ok := fakes[cfg.MAC.String()]
		if !ok || cfg.Host != "" {
			return nil, fmt.Errorf("%w: %s", escp.ErrNotFound, cfg.MAC)
		}
		cfg.Host, cfg.SwitchPort, cfg.BroadcastAddr, cfg.Timeout = loopback, f.Port(), loopback, fakeTimeout
		cfg.HostPort = escptest.FreeUDPPort(t)
		return escp.Dial(cfg)
	}
	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(provider.NewWithDialer("regression", dial)()),
	}
	hcl := func(aOn, bOn bool) string {
		return fmt.Sprintf(`provider "tplinkeasysmart" {
  switches = {
    a = { mac = %q, username = %q, password = %q }
    b = { mac = %q, username = %q, password = %q }
  }
}
resource "tplinkeasysmart_led" "a" {
  switch  = "a"
  enabled = %t
}
resource "tplinkeasysmart_led" "b" {
  switch  = "b"
  enabled = %t
}
`, macA, escptest.Username, escptest.Password, macB, escptest.Username, escptest.Password, aOn, bOn)
	}
	led := func(f *escptest.Switch, want bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			c, err := escp.Dial(escp.Config{Host: loopback, SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: loopback, Timeout: fakeTimeout, Username: escptest.Username, Password: escptest.Password})
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }() // read-only client at the end of the check
			if got, err := c.LED(context.Background()); err != nil || got != want {
				return fmt.Errorf("LED = %t, err %w, want %t", got, err, want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories, Steps: []resource.TestStep{
		{Config: hcl(false, true), Check: resource.ComposeTestCheckFunc(led(a, false), led(b, true))},
		{Config: hcl(true, false), Check: resource.ComposeTestCheckFunc(led(a, true), led(b, false))},
		{Config: hcl(true, true), Check: resource.ComposeTestCheckFunc(led(a, true), led(b, true))},
	}})
}
