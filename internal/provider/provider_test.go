package provider

import (
	"context"
	"regexp"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// fakeClient is an in-memory switch used instead of the UDP transport.
type fakeClient struct {
	mu      sync.Mutex
	led     bool
	sets    int
	failSet error
}

func (f *fakeClient) Info(context.Context) (escp.Info, error) {
	return escp.Info{Model: "TL-SG108E", Hostname: "TL-SG108E-01", MAC: "00:00:5e:00:53:01",
		Firmware: "1.0.0 Build 20230218 Rel.50633", Hardware: "TL-SG108E 6.0",
		IP: "192.0.2.120", Netmask: "255.255.255.0", Gateway: "192.0.2.1", DHCP: true}, nil
}

func (f *fakeClient) Ports(context.Context) ([]escp.Port, error) {
	return []escp.Port{
		{ID: 1, Enabled: true, LAG: 1, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
		{ID: 3, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"},
	}, nil
}

func (f *fakeClient) PortStatistics(context.Context) ([]escp.PortStats, error) {
	return []escp.PortStats{{ID: 1, Enabled: true, Link: "1000Mbps-full", TxGood: 4000000000, RxGood: 7, RxBad: 1}}, nil
}

func (f *fakeClient) GetLED(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.led, nil
}

func (f *fakeClient) SetLED(_ context.Context, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSet != nil {
		return f.failSet
	}
	f.led = on
	f.sets++
	return nil
}

func (f *fakeClient) set(on bool) { f.mu.Lock(); f.led = on; f.mu.Unlock() }

func factories(f SwitchClient) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{
			version:   "test",
			newClient: func(escp.Config) (SwitchClient, error) { return f, nil },
		}),
	}
}

const providerBlock = `
provider "tplinkeasysmart" {
  host     = "192.0.2.120"
  username = "admin"
  password = "x"
}
`

func ledConfig(enabled string) string {
	return providerBlock + `
resource "tplinkeasysmart_led" "this" {
  enabled = ` + enabled + `
}
`
}

func TestAccLEDLifecycle(t *testing.T) {
	f := &fakeClient{led: true}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{
				Config: ledConfig("false"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_led.this", "id", "led"),
					resource.TestCheckResourceAttr("tplinkeasysmart_led.this", "enabled", "false"),
					func(*terraformState) error { return expectLED(f, false) },
				),
			},
			{
				Config: ledConfig("true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_led.this", "enabled", "true"),
					func(*terraformState) error { return expectLED(f, true) },
				),
			},
			{ResourceName: "tplinkeasysmart_led.this", ImportState: true, ImportStateId: "led", ImportStateVerify: true},
		},
	})
}

func TestAccLEDDriftIsDetected(t *testing.T) {
	f := &fakeClient{led: true}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: ledConfig("true")},
			{
				PreConfig:          func() { f.set(false) }, // someone toggles it in the web UI
				Config:             ledConfig("true"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: ledConfig("true"), Check: func(*terraformState) error { return expectLED(f, true) }},
		},
	})
}

func TestAccLEDImportRejectsOtherIDs(t *testing.T) {
	f := &fakeClient{led: true}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: ledConfig("true")},
			{ResourceName: "tplinkeasysmart_led.this", ImportState: true, ImportStateId: "nope",
				ExpectError: regexp.MustCompile(`only valid id is "led"`)},
		},
	})
}

func TestAccSystemInfoDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_system_info" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "model", "TL-SG108E"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "mac_address", "00:00:5e:00:53:01"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "hardware_version", "TL-SG108E 6.0"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "dhcp", "true"),
			),
		}},
	})
}

func TestAccMissingCredentials(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config:      `provider "tplinkeasysmart" {}` + "\n" + `data "tplinkeasysmart_system_info" "this" {}`,
			ExpectError: regexp.MustCompile(`Missing switch host`),
		}},
	})
}

func TestAccPortsDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_ports" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_ports.this", "ports.#", "2"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_ports.this", "ports.0.id", "1"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_ports.this", "ports.0.lag", "1"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_ports.this", "ports.0.actual_speed", "1000Mbps-full"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_ports.this", "ports.1.actual_speed", "link-down"),
			),
		}},
	})
}

func TestAccPortStatisticsDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_port_statistics" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_port_statistics.this", "ports.0.link", "1000Mbps-full"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_port_statistics.this", "ports.0.tx_good_packets", "4000000000"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_port_statistics.this", "ports.0.rx_bad_packets", "1"),
			),
		}},
	})
}
