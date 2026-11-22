package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
)

// silentFactories runs the provider with real escp clients pointed at sw, without retries, so that
// the datagrams of one request are the same ones a direct client sends (datagramsOfOneRequest).
func silentFactories(t *testing.T, sw *escptest.Switch) map[string]func() (tfprotov6.ProviderServer, error) {
	t.Helper()
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{
			version: "test",
			newClient: func(cfg escp.Config) (SwitchClient, error) {
				cfg.SwitchPort = sw.UDPPort()
				cfg.HostPort = escptest.FreeUDPPort(t)
				cfg.BroadcastAddr = escptest.Loopback()
				cfg.Retries = escp.NoRetries
				c, err := escp.Dial(cfg)
				if err != nil {
					return nil, err
				}
				t.Cleanup(func() { _ = c.Close() })
				return c, nil
			},
		}),
	}
}

// The measurement behind #222, as a test: twelve data sources read at once (Terraform's default is
// ten at a time) from a switch that does not answer cost the datagrams of one request, not of one
// per data source, and the walk ends with the latch's error. The count of datagrams is the bound;
// there is no clock in the assertion.
func TestAccParallelDataSourcesOnASilentSwitchAreAskedOnce(t *testing.T) {
	failIfStuck(t, hangBound)
	const sources = 12
	want := datagramsOfOneRequest(t, readInfo) // the data source starts with the Info read

	var cfg strings.Builder
	cfg.WriteString(realClientProvider("  timeout_seconds = 1\n"))
	for i := range sources {
		fmt.Fprintf(&cfg, "\ndata \"tplinkeasysmart_system_info\" \"s%d\" {}\n", i)
	}
	sw := escptest.Start(t)
	sw.Mute()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: silentFactories(t, sw),
		Steps: []resource.TestStep{{
			Config:      cfg.String(),
			ExpectError: regexp.MustCompile(`did not answer earlier in this run`),
		}},
	})
	if got := sw.Received(); got != want {
		t.Fatalf("%d datagram(s) for %d data sources of a silent switch, want the %d of one request", got, sources, want)
	}
}
