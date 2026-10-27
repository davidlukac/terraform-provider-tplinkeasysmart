package regression

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// Hosts the two fakes are configured under. Both are loopback names for the one machine; the
// dialer below sends each to its own fake, so the provider sees two different switches.
const (
	hostA = "127.0.0.1"
	hostB = "127.0.0.2"
)

// The multi-switch model: one provider, a `switches` map, and `switch = "<name>"` on each resource.
// Two fakes stand in for two switches (never the hardware: a switch is only ever reached through
// its own target). Each switch gets its own setting, a change to one configuration writes to that
// switch only, and a change made behind Terraform's back to one shows up on that one.
func TestScenarioMultiSwitch(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("scenarios need TF_ACC=1 (make regression)")
	}
	if target, err := LoadTarget(map[string]string{EnvTarget: os.Getenv(EnvTarget)}); err != nil || target.Real() {
		t.Skip("the multi-switch scenario runs on the fakes only")
	}
	a, b := startFake(t), startFake(t)
	fakes := map[string]*escptest.Switch{hostA: a, hostB: b}
	open := func(cfg escp.Config) (*escp.Client, error) {
		f, ok := fakes[cfg.Host]
		if !ok {
			return nil, fmt.Errorf("no fake for host %q", cfg.Host)
		}
		cfg.Host, cfg.SwitchPort, cfg.BroadcastAddr, cfg.Timeout = "127.0.0.1", f.Port(), escptest.Loopback, fakeTimeout
		cfg.HostPort = escptest.FreeUDPPort(t)
		return escp.Dial(cfg)
	}
	dial := func(cfg escp.Config) (provider.SwitchClient, error) { return open(cfg) }
	client := func(host string) *escp.Client {
		c, err := open(escp.Config{Host: host, Username: escptest.Username, Password: escptest.Password})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	ca, cb := client(hostA), client(hostB)
	ctx := context.Background()
	origA, err := ca.LED(ctx)
	if err != nil {
		t.Fatal(err)
	}
	origB, err := cb.LED(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !origA || !origB {
		t.Fatalf("the modelled switches start with the LED on, got a=%t b=%t", origA, origB)
	}

	providerHCL := fmt.Sprintf(`provider "tplinkeasysmart" {
  switches = {
    a = { host = %q, username = %q, password = %q }
    b = { host = %q, username = %q, password = %q }
  }
}
`, hostA, escptest.Username, escptest.Password, hostB, escptest.Username, escptest.Password)
	leds := func(aOn, bOn bool) string {
		return providerHCL + fmt.Sprintf(`resource "tplinkeasysmart_led" "a" {
  switch  = "a"
  enabled = %t
}

resource "tplinkeasysmart_led" "b" {
  switch  = "b"
  enabled = %t
}
`, aOn, bOn)
	}
	led := func(c *escp.Client, want bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got, err := c.LED(ctx); err != nil || got != want {
				return fmt.Errorf("LED = %t, err %w, want %t", got, err, want)
			}
			return nil
		}
	}
	var writesA, writesB int
	mark := func(*terraform.State) error { writesA, writesB = a.WriteCount(), b.WriteCount(); return nil }
	only := func(wantA, wantB bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := a.WriteCount() > writesA; got != wantA {
				return fmt.Errorf("switch a written = %t, want %t", got, wantA)
			}
			if got := b.WriteCount() > writesB; got != wantB {
				return fmt.Errorf("switch b written = %t, want %t", got, wantB)
			}
			return nil
		}
	}
	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(provider.NewWithDialer("regression", dial)()),
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: factories, Steps: []resource.TestStep{
		// Each switch gets its own value.
		{Config: leds(false, true), Check: resource.ComposeTestCheckFunc(led(ca, false), led(cb, true))},
		// Only a's configuration changes: b is not written.
		{PreConfig: func() { _ = mark(nil) }, Config: leds(true, true),
			Check: resource.ComposeTestCheckFunc(led(ca, true), led(cb, true), only(true, false))},
		// Only b's configuration changes: a is not written.
		{PreConfig: func() { _ = mark(nil) }, Config: leds(true, false),
			Check: resource.ComposeTestCheckFunc(led(ca, true), led(cb, false), only(false, true))},
		// A change behind Terraform's back to b is a plan difference; a stays clean.
		{PreConfig: func() {
			if err := cb.SetLED(ctx, true); err != nil {
				t.Fatal(err)
			}
		}, Config: leds(true, false), PlanOnly: true, ExpectNonEmptyPlan: true},
		// Restore both explicitly (destroy only forgets).
		{Config: leds(origA, origB), Check: resource.ComposeTestCheckFunc(led(ca, origA), led(cb, origB))},
	}})
}
