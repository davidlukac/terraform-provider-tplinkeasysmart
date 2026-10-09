package provider

import (
	"context"
	"fmt"
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
	igmp    escp.IGMP
	loop    bool
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

func (f *fakeClient) LAGs(context.Context) ([]escp.LAG, error) {
	return []escp.LAG{{ID: 1, Ports: []int{1, 2}}, {ID: 2, Ports: []int{}}}, nil
}

func (f *fakeClient) IGMP(context.Context) (escp.IGMP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.igmp
	g.Groups = []escp.IGMPGroup{{IP: "239.255.255.250", VLAN: 1, Ports: []int{1, 4}}}
	return g, nil
}

func (f *fakeClient) SetIGMP(_ context.Context, enabled, suppression bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.igmp = escp.IGMP{Enabled: enabled, ReportSuppression: suppression}
	return nil
}

func (f *fakeClient) LoopPrevention(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loop, nil
}

func (f *fakeClient) SetLoopPrevention(_ context.Context, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loop = on
	return nil
}

func (f *fakeClient) PortMirror(context.Context) (escp.Mirror, error) {
	return escp.Mirror{Enabled: true, MirrorPort: 8, IngressPorts: []int{1, 2}, EgressPorts: []int{}}, nil
}

func (f *fakeClient) MTUVLAN(context.Context) (escp.MTUVLAN, error) {
	return escp.MTUVLAN{Enabled: true, UplinkPort: 8}, nil
}

func (f *fakeClient) PortBasedVLAN(context.Context) (escp.PortBasedVLAN, error) {
	return escp.PortBasedVLAN{Enabled: true, MaxVLAN: 8, VLANs: []escp.PortVLAN{{ID: 1, Ports: []int{1, 2, 3}}}}, nil
}

func (f *fakeClient) VLAN8021Q(context.Context) (escp.Dot1Q, error) {
	return escp.Dot1Q{Enabled: true, MaxVLAN: 32,
		VLANs: []escp.VLAN8021Q{{ID: 10, Name: "iot", Tagged: []int{8}, Untagged: []int{1, 2}}},
		PVIDs: []escp.PortPVID{{Port: 1, PVID: 10}, {Port: 8, PVID: 1}}}, nil
}

func (f *fakeClient) QoS(context.Context) (escp.QoS, error) {
	return escp.QoS{Mode: 2, ModeName: "dscp-802.1p", Priorities: []escp.PortPriority{{Port: 1, Priority: 1}, {Port: 2, Priority: 4}}}, nil
}

func (f *fakeClient) Bandwidth(context.Context) ([]escp.PortBandwidth, error) {
	return []escp.PortBandwidth{{Port: 1}, {Port: 2, IngressKbps: 4096, EgressKbps: 8192}}, nil
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

func TestAccLAGsDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_lags" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_lags.this", "lags.#", "2"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_lags.this", "lags.0.ports.#", "2"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_lags.this", "lags.0.ports.1", "2"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_lags.this", "lags.1.ports.#", "0"),
			),
		}},
	})
}

func igmpConfig(enabled string) string {
	return providerBlock + `
resource "tplinkeasysmart_igmp_snooping" "this" {
  enabled = ` + enabled + `
}
`
}

func TestAccIGMPSnoopingLifecycle(t *testing.T) {
	f := &fakeClient{igmp: escp.IGMP{Enabled: true}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: igmpConfig("true"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_igmp_snooping.this", "id", "igmp_snooping"),
				resource.TestCheckResourceAttr("tplinkeasysmart_igmp_snooping.this", "enabled", "true"),
				resource.TestCheckResourceAttr("tplinkeasysmart_igmp_snooping.this", "report_message_suppression", "false"),
				resource.TestCheckResourceAttr("tplinkeasysmart_igmp_snooping.this", "multicast_groups.0.ip", "239.255.255.250"),
				resource.TestCheckResourceAttr("tplinkeasysmart_igmp_snooping.this", "multicast_groups.0.ports.1", "4"),
			)},
			{Config: igmpConfig("false"), Check: func(*terraformState) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.igmp.Enabled {
					return fmt.Errorf("IGMP still enabled on the switch")
				}
				return nil
			}},
			{ResourceName: "tplinkeasysmart_igmp_snooping.this", ImportState: true, ImportStateId: "igmp_snooping", ImportStateVerify: true},
		},
	})
}

func TestAccLoopPreventionLifecycle(t *testing.T) {
	f := &fakeClient{loop: true}
	cfg := func(v string) string {
		return providerBlock + `resource "tplinkeasysmart_loop_prevention" "this" { enabled = ` + v + ` }`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg("true"), Check: resource.TestCheckResourceAttr("tplinkeasysmart_loop_prevention.this", "enabled", "true")},
			{Config: cfg("false"), Check: func(*terraformState) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.loop {
					return fmt.Errorf("loop prevention still on")
				}
				return nil
			}},
			{ResourceName: "tplinkeasysmart_loop_prevention.this", ImportState: true, ImportStateId: "loop_prevention", ImportStateVerify: true},
		},
	})
}

func TestAccPortMirrorDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_port_mirror" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_port_mirror.this", "mirror_port", "8"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_port_mirror.this", "ingress_ports.#", "2"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_port_mirror.this", "egress_ports.#", "0"),
			),
		}},
	})
}

func TestAccVLANMTUDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_vlan_mtu" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_mtu.this", "enabled", "true"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_mtu.this", "uplink_port", "8"),
			),
		}},
	})
}

func TestAccVLANPortBasedDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_vlan_port_based" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_port_based.this", "enabled", "true"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_port_based.this", "max_vlans", "8"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_port_based.this", "vlans.0.ports.#", "3"),
			),
		}},
	})
}

func TestAccVLAN8021QDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_vlan_8021q" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_8021q.this", "max_vlans", "32"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_8021q.this", "vlans.0.name", "iot"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_8021q.this", "vlans.0.tagged_ports.0", "8"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_vlan_8021q.this", "pvids.0.pvid", "10"),
			),
		}},
	})
}

func TestAccQoSDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_qos" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_qos.this", "mode", "2"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_qos.this", "mode_name", "dscp-802.1p"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_qos.this", "port_priorities.1.priority", "4"),
			),
		}},
	})
}

func TestAccBandwidthControlDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_bandwidth_control" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_bandwidth_control.this", "ports.0.ingress_kbps", "0"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_bandwidth_control.this", "ports.1.egress_kbps", "8192"),
			),
		}},
	})
}
