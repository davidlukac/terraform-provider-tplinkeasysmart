package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
)

// The other firmware-gate tests put a double (gatedClient) in front of an in-memory
// switch, so they prove the provider's wiring and wording but not the client's part.
// These tests use no double on the client side: Terraform drives the provider, the
// provider builds a real escp.Client with the gate it configures, and the client talks
// UDP to the fake switch. They prove that a refused write reaches the wire as nothing.

// realClientFactories runs the provider with real escp clients pointed at sw.
func realClientFactories(t *testing.T, sw *escptest.Switch) map[string]func() (tfprotov6.ProviderServer, error) {
	t.Helper()
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{
			version: "test",
			newClient: func(cfg escp.Config) (SwitchClient, error) {
				cfg.SwitchPort = sw.Port()
				cfg.HostPort = escptest.FreeUDPPort(t) // the client shares its socket, so a free port is enough
				cfg.BroadcastAddr = escptest.Loopback()
				c, err := escp.Dial(cfg)
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = c.Close() }) // best effort at the end of the test
				return c, nil
			},
		}),
	}
}

// realClientProvider is a provider block for the fake switch; extra is added to it verbatim.
func realClientProvider(extra string) string {
	return fmt.Sprintf(`
provider "tplinkeasysmart" {
  host     = "127.0.0.1"
  username = %q
  password = %q
%s}
`, escptest.Username, escptest.Password, extra)
}

const realClientLEDOff = `
resource "tplinkeasysmart_led" "this" {
  enabled = false
}
`

func TestAccRealClientRefusesAWriteOnUnverifiedFirmwareAndSendsNothing(t *testing.T) {
	sw := escptest.Start(t)
	sw.SetFirmware(unverifiedFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: realClientFactories(t, sw),
		Steps: []resource.TestStep{{
			Config:      realClientProvider("") + realClientLEDOff,
			ExpectError: regexp.MustCompile(`(?s)refusing to write: firmware build 20990101 has not\s+been\s+verified.*allow_unverified_firmware`),
		}},
	})
	if sets := sw.AllSets(); len(sets) != 0 {
		t.Fatalf("the switch received %d write(s) despite the refusal: %v", len(sets), sets)
	}
	if led, writes := sw.LED(), sw.WriteCount(); !led || writes != 0 {
		t.Fatalf("the LED must be untouched: led=%v writes=%d", led, writes)
	}
}

func TestAccRealClientReadsWorkOnUnverifiedFirmware(t *testing.T) {
	sw := escptest.Start(t)
	sw.SetFirmware(unverifiedFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: realClientFactories(t, sw),
		Steps: []resource.TestStep{{
			Config: realClientProvider("") + "\ndata \"tplinkeasysmart_system_info\" \"this\" {}\n",
			Check:  resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "firmware_version", unverifiedFirmware),
		}},
	})
}

func TestAccRealClientWritesOnVerifiedFirmwareWithoutOptIn(t *testing.T) {
	sw := escptest.Start(t)
	sw.SetFirmware(newerFirmware) // 20250710 is verified; the default 20230218 is covered by the next test
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: realClientFactories(t, sw),
		Steps:                    []resource.TestStep{{Config: realClientProvider("") + realClientLEDOff}},
	})
	assertLEDOffWasSentOnce(t, sw)
}

func TestAccRealClientWritesOnTheDefaultFirmware(t *testing.T) {
	sw := escptest.Start(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: realClientFactories(t, sw),
		Steps:                    []resource.TestStep{{Config: realClientProvider("") + realClientLEDOff}},
	})
	assertLEDOffWasSentOnce(t, sw)
}

func TestAccRealClientOptInWritesOnUnverifiedFirmware(t *testing.T) {
	sw := escptest.Start(t)
	sw.SetFirmware(unverifiedFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: realClientFactories(t, sw),
		Steps:                    []resource.TestStep{{Config: realClientProvider("  allow_unverified_firmware = true\n") + realClientLEDOff}},
	})
	assertLEDOffWasSentOnce(t, sw)
}

// assertLEDOffWasSentOnce checks what reached the wire: exactly one write, and the fake
// only turns its LED off on a write of the LED setting.
func assertLEDOffWasSentOnce(t *testing.T, sw *escptest.Switch) {
	t.Helper()
	if sets := sw.AllSets(); len(sets) != 1 {
		t.Fatalf("want exactly one write, the switch received %d: %v", len(sets), sets)
	}
	if sw.LED() {
		t.Fatal("the LED write did not arrive: the LED is still on")
	}
}
