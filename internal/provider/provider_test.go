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
	bw      map[int][2]int
	qos     *escp.QoS
	ports   []escp.Port
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

func (f *fakeClient) SetPort(_ context.Context, id int, enabled bool, speed string, flow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.ports {
		if f.ports[i].ID == id {
			f.ports[i].Enabled, f.ports[i].ConfiguredSpeed, f.ports[i].ConfiguredFlowControl = enabled, speed, flow
			f.sets++
		}
	}
	return nil
}

func (f *fakeClient) Ports(context.Context) ([]escp.Port, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ports != nil {
		return append([]escp.Port(nil), f.ports...), nil
	}
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

func (f *fakeClient) SetQoSMode(_ context.Context, mode int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.qos.Mode, f.qos.ModeName = mode, escp.QoSModeNames[mode]
	return nil
}

func (f *fakeClient) SetPortPriorities(_ context.Context, ch map[int]int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.qos.Mode != 0 {
		return nil // like the real switch: accepted but ignored outside port-based mode
	}
	for i := range f.qos.Priorities {
		if v, ok := ch[f.qos.Priorities[i].Port]; ok {
			f.qos.Priorities[i].Priority = v
		}
	}
	return nil
}

func (f *fakeClient) QoS(context.Context) (escp.QoS, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.qos != nil {
		q := *f.qos
		q.Priorities = append([]escp.PortPriority(nil), f.qos.Priorities...)
		return q, nil
	}
	return escp.QoS{Mode: 2, ModeName: "dscp-802.1p", Priorities: []escp.PortPriority{{Port: 1, Priority: 1}, {Port: 2, Priority: 4}}}, nil
}

func (f *fakeClient) SetBandwidth(_ context.Context, port, in, eg int) error {
	if in%64 != 0 || eg%64 != 0 {
		return fmt.Errorf("invalid rate: use 0 or a multiple of 64")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bw == nil {
		f.bw = map[int][2]int{}
	}
	f.bw[port] = [2]int{in, eg}
	f.sets++
	return nil
}

func (f *fakeClient) Bandwidth(context.Context) ([]escp.PortBandwidth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.bw != nil {
		var rows []escp.PortBandwidth
		for _, p := range []int{1, 4, 7} {
			rows = append(rows, escp.PortBandwidth{Port: p, IngressKbps: f.bw[p][0], EgressKbps: f.bw[p][1]})
		}
		return rows, nil
	}
	return []escp.PortBandwidth{{Port: 1}, {Port: 2, IngressKbps: 4096, EgressKbps: 8192}}, nil
}

func (f *fakeClient) StormControl(context.Context) ([]escp.PortStorm, error) {
	return []escp.PortStorm{{Port: 1}, {Port: 2, Broadcast: true, RateKbps: 5000, Enabled: true}}, nil
}

func (f *fakeClient) System(context.Context) (escp.System, error) {
	return escp.System{PortCount: 8, AutoSave: true}, nil
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
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "port_count", "8"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "auto_save", "true"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "factory_default", "false"),
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

func TestAccStormControlDataSource(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(&fakeClient{}),
		Steps: []resource.TestStep{{
			Config: providerBlock + `data "tplinkeasysmart_storm_control" "this" {}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.tplinkeasysmart_storm_control.this", "ports.0.enabled", "false"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_storm_control.this", "ports.1.broadcast", "true"),
				resource.TestCheckResourceAttr("data.tplinkeasysmart_storm_control.this", "ports.1.rate_kbps", "5000"),
			),
		}},
	})
}

func testPorts() []escp.Port {
	return []escp.Port{
		{ID: 1, Enabled: true, LAG: 1, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
		{ID: 4, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
		{ID: 7, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"},
	}
}

func portConfig(port int, extra string) string {
	return providerBlock + fmt.Sprintf(`
resource "tplinkeasysmart_port" "p" {
  port = %d
%s}
`, port, extra)
}

func TestAccPortLifecycleOnUnusedPort(t *testing.T) {
	f := &fakeClient{ports: testPorts()}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: portConfig(7, "  flow_control = true\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "flow_control", "true"),
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "speed", "auto"),
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "actual_speed", "link-down"),
			)},
			{Config: portConfig(7, "  speed = \"100Mbps-full\"\n  enabled = false\n"), Check: func(*terraformState) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if p := f.ports[2]; p.Enabled || p.ConfiguredSpeed != "100Mbps-full" || p.ConfiguredFlowControl {
					return fmt.Errorf("port 7 on the switch: %+v", p)
				}
				return nil
			}},
			{ResourceName: "tplinkeasysmart_port.p", ImportState: true, ImportStateId: "7", ImportStateVerify: true,
				ImportStateVerifyIdentifierAttribute: "port"},
		},
	})
}

func TestAccPortGuardBlocksUsedPorts(t *testing.T) {
	for _, tc := range []struct {
		port int
		want string
	}{{1, `member of LAG1`}, {4, `has a link up`}} {
		f := &fakeClient{ports: testPorts()}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps: []resource.TestStep{{
				Config:      portConfig(tc.port, "  enabled = false\n"),
				ExpectError: regexp.MustCompile(tc.want),
			}},
		})
		if f.sets != 0 {
			t.Fatalf("port %d: guard must prevent any write, got %d", tc.port, f.sets)
		}
	}
}

func TestAccPortGuardOverride(t *testing.T) {
	f := &fakeClient{ports: testPorts()}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: portConfig(4, "  flow_control = true\n  allow_disruptive = true\n"),
			Check:  resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "flow_control", "true"),
		}},
	})
}

func newFakeQoS() *fakeClient {
	q := escp.QoS{Mode: 2, ModeName: "dscp-802.1p"}
	for p := 1; p <= 8; p++ {
		q.Priorities = append(q.Priorities, escp.PortPriority{Port: p, Priority: 1})
	}
	return &fakeClient{qos: &q}
}

func TestAccQoSResourceLifecycle(t *testing.T) {
	f := newFakeQoS()
	cfg := func(body string) string {
		return providerBlock + "resource \"tplinkeasysmart_qos\" \"this\" {\n" + body + "}\n"
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg("  mode = \"dscp-802.1p\"\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "mode", "dscp-802.1p"),
				resource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "port_priorities.%", "0"),
			)},
			{Config: cfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 3 }\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "mode", "port-based"),
				resource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "port_priorities.7", "3"),
				resource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "port_priorities.%", "1"),
			)},
			{Config: cfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 3 }\n"), PlanOnly: true}, // stays clean
			{ResourceName: "tplinkeasysmart_qos.this", ImportState: true, ImportStateId: "qos", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"port_priorities"}},
		},
	})
}

func TestAccQoSResourceRejectsPrioritiesOutsidePortBased(t *testing.T) {
	f := newFakeQoS()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config:      providerBlock + "resource \"tplinkeasysmart_qos\" \"this\" {\n  mode = \"802.1p\"\n  port_priorities = { \"7\" = 2 }\n}\n",
			ExpectError: regexp.MustCompile(`only take effect when mode is port-based`),
		}},
	})
	if f.qos.Mode != 2 {
		t.Fatal("nothing may be written when the configuration is rejected")
	}
}

func bwConfig(port int, body string) string {
	return providerBlock + fmt.Sprintf("resource \"tplinkeasysmart_bandwidth_limit\" \"b\" {\n  port = %d\n%s}\n", port, body)
}

func TestAccBandwidthLimitLifecycle(t *testing.T) {
	f := &fakeClient{ports: testPorts(), bw: map[int][2]int{}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: bwConfig(7, "  ingress_kbps = 1024\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_bandwidth_limit.b", "ingress_kbps", "1024"),
				resource.TestCheckResourceAttr("tplinkeasysmart_bandwidth_limit.b", "egress_kbps", "0"),
			)},
			{Config: bwConfig(7, "  ingress_kbps = 0\n  egress_kbps = 64\n"), Check: func(*terraformState) error {
				f.mu.Lock()
				defer f.mu.Unlock()
				if f.bw[7] != [2]int{0, 64} {
					return fmt.Errorf("switch has %v", f.bw[7])
				}
				return nil
			}},
			{ResourceName: "tplinkeasysmart_bandwidth_limit.b", ImportState: true, ImportStateId: "7", ImportStateVerify: true,
				ImportStateVerifyIdentifierAttribute: "port"},
		},
	})
}

func TestAccBandwidthLimitGuardAndValidation(t *testing.T) {
	f := &fakeClient{ports: testPorts(), bw: map[int][2]int{}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: bwConfig(4, "  ingress_kbps = 1024\n"), ExpectError: regexp.MustCompile(`has a link up`)},
			{Config: bwConfig(1, "  egress_kbps = 1024\n"), ExpectError: regexp.MustCompile(`member of LAG1`)},
			{Config: bwConfig(7, "  ingress_kbps = 100\n"), ExpectError: regexp.MustCompile(`multiple of 64`)},
		},
	})
	if f.sets != 0 {
		t.Fatalf("no write may happen for blocked or invalid requests, got %d", f.sets)
	}
}
