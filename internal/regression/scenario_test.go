package regression

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// The scenarios run through a real Terraform binary against the target chosen by the environment
// (the fake by default). Run them with `make regression` (TF_ACC=1); `make regression-live`
// aims them at switch 03. Each scenario changes a setting, checks it by reading the switch
// directly (not through the provider), and puts the original value back with an explicit
// configuration, because destroying a resource only forgets it. The last check of every
// scenario is the readout of the whole switch against the one taken before the first scenario.

// scenario runs the two steps every scenario has: change and verify through the library, then
// restore the original values by configuration and prove the readout is identical.
func (s *Session) scenario(t *testing.T, change, restore string, verifyChange resource.TestCheckFunc) {
	t.Helper()
	s.scenarioSteps(t, change, verifyChange, restore)
}

// scenarioSteps is scenario with several restore configurations, applied in order, for settings
// that cannot go back in one step (the priorities of a port must be put back while the mode that
// allows them is still set). The readout is compared after the last one.
func (s *Session) scenarioSteps(t *testing.T, change string, verifyChange resource.TestCheckFunc, restore ...string) {
	t.Helper()
	s.scenarioWithProvider(t, s.ProviderHCL(), change, verifyChange, restore...)
}

// scenarioWithProvider is scenarioSteps with the provider block chosen by the caller (the switch
// named by its MAC address, for instance).
func (s *Session) scenarioWithProvider(t *testing.T, providerHCL, change string, verifyChange resource.TestCheckFunc, restore ...string) {
	t.Helper()
	steps := []resource.TestStep{{Config: providerHCL + change, Check: verifyChange}}
	for i, cfg := range restore {
		step := resource.TestStep{Config: providerHCL + cfg}
		if i == len(restore)-1 {
			step.Check = s.restoredCheck(t)
		}
		steps = append(steps, step)
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: steps})
}

// restoredCheck fails with the differing settings named when the switch is not as it was.
func (s *Session) restoredCheck(t *testing.T) resource.TestCheckFunc {
	return func(*terraform.State) error {
		diff, err := s.Restored(context.Background())
		if err != nil {
			return err
		}
		if len(diff) > 0 {
			return fmt.Errorf("the switch is not as it was before the run: %q", diff)
		}
		return nil
	}
}

func openForScenario(t *testing.T) *Session {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("scenarios need TF_ACC=1 (make regression)")
	}
	s := Open(t, os.Getenv)
	t.Log(s.Describe())
	return s
}

func TestScenarioLED(t *testing.T) {
	s := openForScenario(t)
	orig, err := s.Client.LED(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flipped := !orig
	cfg := func(on bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_led\" \"this\" {\n  enabled = %t\n}\n", on)
	}
	s.scenario(t, cfg(flipped), cfg(orig), func(*terraform.State) error {
		got, err := s.Client.LED(context.Background())
		if err != nil || got != flipped {
			return fmt.Errorf("LED on the switch = %t, err %w, want %t", got, err, flipped)
		}
		return nil
	})
}

func TestScenarioIGMPSnooping(t *testing.T) {
	s := openForScenario(t)
	orig, err := s.Client.IGMP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	enabled, suppression := orig.Enabled, orig.ReportSuppression
	cfg := func(e, r bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_igmp_snooping\" \"this\" {\n  enabled                    = %t\n  report_message_suppression = %t\n}\n", e, r)
	}
	s.scenario(t, cfg(!enabled, !suppression), cfg(enabled, suppression), func(*terraform.State) error {
		got, err := s.Client.IGMP(context.Background())
		if err != nil || got.Enabled != !enabled || got.ReportSuppression != !suppression {
			return fmt.Errorf("IGMP on the switch = %+v, err %w, want enabled=%t suppression=%t", got, err, !enabled, !suppression)
		}
		return nil
	})
}

// Report suppression is the one value the switch keeps while its feature is off (measured on
// firmware 20250710, internal/escp/PROTOCOL.md): it can be set while snooping is off and is still
// there when snooping is switched on. Every other value of an inactive feature is dropped.
func TestScenarioIGMPSuppressionStagedWhileSnoopingIsOff(t *testing.T) {
	s := openForScenario(t)
	orig, err := s.Client.IGMP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(e, r bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_igmp_snooping\" \"this\" {\n  enabled                    = %t\n  report_message_suppression = %t\n}\n", e, r)
	}
	is := func(e, r bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got, err := s.Client.IGMP(context.Background())
			if err != nil || got.Enabled != e || got.ReportSuppression != r {
				return fmt.Errorf("IGMP on the switch = %+v, err %w, want enabled=%t suppression=%t", got, err, e, r)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: s.ProviderHCL() + cfg(false, true), Check: is(false, true)},
		{Config: s.ProviderHCL() + cfg(true, true), Check: is(true, true)},
		{Config: s.ProviderHCL() + cfg(orig.Enabled, orig.ReportSuppression), Check: s.restoredCheck(t)},
	}})
}

func TestScenarioLoopPrevention(t *testing.T) {
	s := openForScenario(t)
	orig, err := s.Client.LoopPrevention(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(on bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_loop_prevention\" \"this\" {\n  enabled = %t\n}\n", on)
	}
	s.scenario(t, cfg(!orig), cfg(orig), func(*terraform.State) error {
		got, err := s.Client.LoopPrevention(context.Background())
		if err != nil || got != !orig {
			return fmt.Errorf("loop prevention on the switch = %t, err %w, want %t", got, err, !orig)
		}
		return nil
	})
}

func TestScenarioHostname(t *testing.T) {
	s := openForScenario(t)
	info, err := s.Client.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := func(name string) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_system\" \"this\" {\n  hostname = %q\n}\n", name)
	}
	const renamed = "regression-test"
	s.scenario(t, cfg(renamed), cfg(info.Hostname), func(*terraform.State) error {
		got, err := s.Client.Info(context.Background())
		if err != nil || got.Hostname != renamed {
			return fmt.Errorf("hostname on the switch = %q, err %w, want %q", got.Hostname, err, renamed)
		}
		return nil
	})
}

func TestScenarioQoS(t *testing.T) {
	s := openForScenario(t)
	port := s.Target.TestPorts[0]
	orig, err := s.Client.QoS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var origPrio escp.Priority
	for _, p := range orig.Priorities {
		if int(p.Port) == port {
			origPrio = p.Priority
		}
	}
	cfg := func(mode escp.QoSMode, prio escp.Priority) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_qos\" \"this\" {\n  mode            = %q\n  port_priorities = { \"%d\" = %d }\n}\n", mode.String(), port, int(prio))
	}
	modeOnly := fmt.Sprintf("resource \"tplinkeasysmart_qos\" \"this\" {\n  mode = %q\n}\n", orig.Mode.String())
	changed := escp.PriorityHighest
	if origPrio == changed {
		changed = escp.PriorityLowest
	}
	s.scenarioSteps(t, cfg(escp.QoSPortBased, changed), func(*terraform.State) error {
		got, err := s.Client.QoS(context.Background())
		if err != nil || got.Mode != escp.QoSPortBased {
			return fmt.Errorf("QoS on the switch = %+v, err %w, want port-based", got, err)
		}
		for _, p := range got.Priorities {
			if int(p.Port) == port && p.Priority != changed {
				return fmt.Errorf("port %d priority = %d, want %d", port, p.Priority, changed)
			}
		}
		return nil
	}, cfg(escp.QoSPortBased, origPrio), modeOnly)
}

func TestScenarioPort(t *testing.T) {
	s := openForScenario(t)
	port := s.Target.TestPorts[0]
	ports, err := s.Client.Ports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var orig escp.Port
	for _, p := range ports {
		if int(p.ID) == port {
			orig = p
		}
	}
	cfg := func(speed escp.Speed, flow bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_port\" \"this\" {\n  port         = %d\n  speed        = %q\n  flow_control = %t\n}\n", port, string(speed), flow)
	}
	changed := escp.Speed100Full
	if orig.ConfiguredSpeed == changed {
		changed = escp.SpeedAuto
	}
	s.scenario(t, cfg(changed, false), cfg(orig.ConfiguredSpeed, orig.ConfiguredFlowControl), func(*terraform.State) error {
		got, err := s.Client.Ports(context.Background())
		if err != nil {
			return err
		}
		for _, p := range got {
			if int(p.ID) == port && p.ConfiguredSpeed != changed {
				return fmt.Errorf("port %d speed = %s, want %s", port, p.ConfiguredSpeed, changed)
			}
		}
		return nil
	})
}

func TestScenarioBandwidthLimit(t *testing.T) {
	s := openForScenario(t)
	port := s.Target.TestPorts[0]
	orig, err := s.Client.Bandwidth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var was escp.PortBandwidth
	for _, b := range orig {
		if int(b.Port) == port {
			was = b
		}
	}
	cfg := func(in, out int) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_bandwidth_limit\" \"this\" {\n  port         = %d\n  ingress_kbps = %d\n  egress_kbps  = %d\n}\n", port, in, out)
	}
	s.scenario(t, cfg(10240, 5120), cfg(was.IngressKbps, was.EgressKbps), func(*terraform.State) error {
		got, err := s.Client.Bandwidth(context.Background())
		if err != nil {
			return err
		}
		for _, b := range got {
			if int(b.Port) == port && (b.IngressKbps != 10240 || b.EgressKbps != 5120) {
				return fmt.Errorf("port %d limits = %+v, want 10240 in, 5120 out", port, b)
			}
		}
		return nil
	})
}

func TestScenarioStormLimit(t *testing.T) {
	s := openForScenario(t)
	port := s.Target.TestPorts[0]
	orig, err := s.Client.StormControl(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var was escp.PortStorm
	for _, p := range orig {
		if int(p.Port) == port {
			was = p
		}
	}
	cfg := func(uu, mc, bc bool, rate int) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_storm_limit\" \"this\" {\n  port            = %d\n  unknown_unicast = %t\n  multicast       = %t\n  broadcast       = %t\n  rate_kbps       = %d\n}\n", port, uu, mc, bc, rate)
	}
	s.scenario(t, cfg(false, true, true, 1024), cfg(was.UnknownUnicast, was.Multicast, was.Broadcast, was.RateKbps), func(*terraform.State) error {
		got, err := s.Client.StormControl(context.Background())
		if err != nil {
			return err
		}
		for _, p := range got {
			if int(p.Port) == port && (!p.Multicast || !p.Broadcast || p.RateKbps != 1024) {
				return fmt.Errorf("port %d storm control = %+v, want multicast and broadcast at 1024", port, p)
			}
		}
		return nil
	})
}

func intSet(ports ...int) string {
	var s []string
	for _, p := range ports {
		s = append(s, strconv.Itoa(p))
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func TestScenarioLAG(t *testing.T) {
	s := openForScenario(t)
	a, b := s.Target.TestPorts[1], s.Target.TestPorts[2] // two ports of LAG 1 (ports 1-4)
	cfg := fmt.Sprintf("resource \"tplinkeasysmart_lag\" \"this\" {\n  group = 1\n  ports = %s\n}\n", intSet(a, b))
	s.scenario(t, cfg, "", func(*terraform.State) error {
		got, err := s.Client.LAGs(context.Background())
		if err != nil {
			return err
		}
		for _, l := range got {
			if l.ID == 1 && fmt.Sprint(l.Ports) == fmt.Sprint([]int{a, b}) {
				return nil
			}
		}
		return fmt.Errorf("LAG 1 not %v on the switch: %v", []int{a, b}, got)
	})
}

// The switch refuses a LAG whose members mix auto-negotiation with a fixed speed (error 7410);
// the provider reports the refusal and writes nothing. The ports are put back to auto by
// configuration afterwards, because removing a port resource from a configuration only forgets it.
func TestScenarioLAGRefusedByTheSwitch(t *testing.T) {
	s := openForScenario(t)
	a, b := s.Target.TestPorts[1], s.Target.TestPorts[2]
	ports := func(speedA escp.Speed) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_port\" \"a\" {\n  port  = %d\n  speed = %q\n}\nresource \"tplinkeasysmart_port\" \"b\" {\n  port  = %d\n  speed = \"auto\"\n}\n", a, string(speedA), b)
	}
	lag := fmt.Sprintf("resource \"tplinkeasysmart_lag\" \"this\" {\n  group      = 1\n  ports      = %s\n  depends_on = [tplinkeasysmart_port.a, tplinkeasysmart_port.b]\n}\n", intSet(a, b))
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: s.Factories(),
		Steps: []resource.TestStep{
			{Config: s.ProviderHCL() + ports(escp.Speed100Full)},
			{Config: s.ProviderHCL() + ports(escp.Speed100Full) + lag, ExpectError: regexp.MustCompile(`7410`)},
			{Config: s.ProviderHCL() + ports(escp.SpeedAuto), Check: s.restoredCheck(t)},
		},
	})
}

func TestScenarioPortMirror(t *testing.T) {
	s := openForScenario(t)
	dest, src := s.Target.TestPorts[3], s.Target.TestPorts[2]
	cfg := func(body string) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_port_mirror\" \"this\" {\n%s}\n", body)
	}
	s.scenario(t, cfg(fmt.Sprintf("  enabled       = true\n  mirror_port   = %d\n  ingress_ports = %s\n", dest, intSet(src))),
		cfg("  enabled = false\n"), func(*terraform.State) error {
			got, err := s.Client.PortMirror(context.Background())
			if err != nil || !got.Enabled || int(got.MirrorPort) != dest || fmt.Sprint(got.IngressPorts) != fmt.Sprint([]int{src}) {
				return fmt.Errorf("mirror on the switch = %+v, err %w, want %d mirrored to %d", got, err, src, dest)
			}
			return nil
		})
}

func TestScenarioPortBasedVLANGroup(t *testing.T) {
	s := openForScenario(t)
	a, b := s.Target.TestPorts[1], s.Target.TestPorts[2]
	cfg := fmt.Sprintf("resource \"tplinkeasysmart_vlan_port_based_group\" \"this\" {\n  vlan  = 2\n  ports = %s\n}\n", intSet(a, b))
	s.scenario(t, cfg, "", func(*terraform.State) error {
		got, err := s.Client.PortBasedVLAN(context.Background())
		if err != nil {
			return err
		}
		for _, v := range got.VLANs {
			if v.ID == 2 && fmt.Sprint(v.Ports) == fmt.Sprint([]int{a, b}) {
				return nil
			}
		}
		return fmt.Errorf("VLAN 2 not %v on the switch: %v", []int{a, b}, got.VLANs)
	})
}

// Guard refusals: writes to the ports that carry the uplink or a device are refused before
// anything is sent. Each refusal is a step of its own, and at the end the switch must not have
// received a single write (the fake counts them; on hardware the readout must be identical).
func TestScenarioGuardRefusals(t *testing.T) {
	s := openForScenario(t)
	prot := s.Target.ProtectedPorts
	if len(prot) < 4 {
		t.Skip("needs four protected ports (the uplink and three device ports)")
	}
	up, d1, d2, d3 := prot[0], prot[1], prot[2], prot[3]
	refused := regexp.MustCompile(`(?i)refus`)
	cases := map[string]string{
		"speed of a device port":          fmt.Sprintf("resource \"tplinkeasysmart_port\" \"x\" {\n  port  = %d\n  speed = \"100Mbps-full\"\n}\n", d1),
		"disabling the uplink":            fmt.Sprintf("resource \"tplinkeasysmart_port\" \"x\" {\n  port    = %d\n  enabled = false\n  speed   = \"auto\"\n}\n", up),
		"bandwidth of a device":           fmt.Sprintf("resource \"tplinkeasysmart_bandwidth_limit\" \"x\" {\n  port         = %d\n  ingress_kbps = 10240\n}\n", d3),
		"storm control":                   fmt.Sprintf("resource \"tplinkeasysmart_storm_limit\" \"x\" {\n  port      = %d\n  broadcast = true\n  rate_kbps = 1024\n}\n", d2),
		"a LAG with the uplink":           fmt.Sprintf("resource \"tplinkeasysmart_lag\" \"x\" {\n  group = 1\n  ports = %s\n}\n", intSet(up, s.Target.TestPorts[0])),
		"a VLAN group with a device port": fmt.Sprintf("resource \"tplinkeasysmart_vlan_port_based_group\" \"x\" {\n  vlan  = 2\n  ports = %s\n}\n", intSet(d1)),
	}
	var writes int
	if s.Fake != nil {
		writes = s.Fake.WriteCount()
	}
	var steps []resource.TestStep
	for _, cfg := range cases {
		steps = append(steps, resource.TestStep{Config: s.ProviderHCL() + cfg, ExpectError: refused})
	}
	steps = append(steps, resource.TestStep{Config: s.ProviderHCL(), Check: func(*terraform.State) error {
		if s.Fake != nil && s.Fake.WriteCount() != writes {
			return fmt.Errorf("the switch received %d writes during the refusals", s.Fake.WriteCount()-writes)
		}
		return s.restoredCheck(t)(nil)
	}})
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: steps})
}

// allow_disruptive is the opt-in for a port that carries a device. The step that applies it
// writes to that port, so on hardware it runs only when TPLINK_REGRESSION_DISRUPT_DEVICE_PORT is
// set to the port number (a deliberate disruption), never by default. The write repeats the
// port's own configuration, and the opt-in is then removed again.
func TestScenarioAllowDisruptive(t *testing.T) {
	s := openForScenario(t)
	port := s.Target.ProtectedPorts[1]
	if s.Target.Real() {
		if s.Target.DisruptPort == 0 {
			t.Skip("writes to a device port: set TPLINK_REGRESSION_DISRUPT_DEVICE_PORT to one of the device ports to run")
		}
		port = s.Target.DisruptPort
	}
	ports, err := s.Client.Ports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var orig escp.Port
	for _, p := range ports {
		if int(p.ID) == port {
			orig = p
		}
	}
	cfg := func(opt string) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_port\" \"x\" {\n  port         = %d\n  speed        = %q\n  flow_control = %t\n%s}\n", port, string(orig.ConfiguredSpeed), orig.ConfiguredFlowControl, opt)
	}
	var afterOptIn int
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: s.Factories(),
		Steps: []resource.TestStep{
			{Config: s.ProviderHCL() + cfg(""), ExpectError: regexp.MustCompile(`(?i)refus`)},
			{Config: s.ProviderHCL() + cfg("  allow_disruptive = true\n"), Check: func(*terraform.State) error {
				if s.Fake != nil {
					afterOptIn = s.Fake.WriteCount()
				}
				return nil
			}},
			// Taking the opt-in away again is saved without a write to the port.
			{Config: s.ProviderHCL() + cfg(""), Check: func(*terraform.State) error {
				if s.Fake != nil && s.Fake.WriteCount() != afterOptIn {
					return fmt.Errorf("removing allow_disruptive wrote to the switch (%d writes, was %d)", s.Fake.WriteCount(), afterOptIn)
				}
				return s.restoredCheck(t)(nil)
			}},
		},
	})
}

// VLAN mode, an 802.1Q VLAN on two test ports and a PVID. Nothing here touches the uplink or the
// management VLAN: VLAN 1 keeps every port untagged. Restoring takes four steps because removing a
// PVID resource only forgets it (its value is put back by configuration first), a VLAN cannot be
// deleted while a port still has it as PVID, and the mode goes back last.
func TestScenarioVLAN8021Q(t *testing.T) {
	s := openForScenario(t)
	ctx := context.Background()
	orig, err := s.Client.VLANMode(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var originalMode string
	switch orig.Mode {
	case escp.VLANModePortBased, escp.VLANModeDot1Q:
		originalMode = fmt.Sprintf("  mode = %q\n", string(orig.Mode))
	case escp.VLANModeMTU:
		originalMode = fmt.Sprintf("  mode            = \"mtu\"\n  mtu_uplink_port = %d\n", orig.UplinkPort)
	default:
		t.Skipf("the switch is in VLAN mode %q, which cannot be written back", orig.Mode)
	}
	dot1q, err := s.Client.Dot1Q(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, b := s.Target.TestPorts[0], s.Target.TestPorts[1]
	origPVID := 1
	for _, p := range dot1q.PVIDs {
		if int(p.Port) == a {
			origPVID = p.PVID
		}
	}
	mode := func(body string) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_vlan_mode\" \"this\" {\n%s  allow_disruptive = true\n}\n", body)
	}
	group := fmt.Sprintf("resource \"tplinkeasysmart_vlan_8021q_group\" \"lab\" {\n  vlan           = 20\n  name           = \"regtest\"\n  untagged_ports = %s\n  depends_on     = [tplinkeasysmart_vlan_mode.this]\n}\n", intSet(a, b))
	pvid := func(id int, after string) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_vlan_8021q_pvid\" \"lab\" {\n  port       = %d\n  pvid       = %d\n  depends_on = [%s]\n}\n", a, id, after)
	}
	const modeOnly, modeAndGroup = "tplinkeasysmart_vlan_mode.this", "tplinkeasysmart_vlan_mode.this, tplinkeasysmart_vlan_8021q_group.lab"
	s.scenarioSteps(t, mode("  mode = \"802.1q\"\n")+group+pvid(20, modeAndGroup), func(*terraform.State) error {
		got, err := s.Client.Dot1Q(ctx)
		if err != nil || !got.Enabled {
			return fmt.Errorf("802.1Q on the switch = %+v, err %w, want it enabled", got.Enabled, err)
		}
		found := false
		for _, v := range got.VLANs {
			found = found || v.ID == 20 && v.Name == "regtest" && fmt.Sprint(v.Untagged) == fmt.Sprint([]int{a, b})
		}
		for _, p := range got.PVIDs {
			if int(p.Port) == a && p.PVID != 20 {
				return fmt.Errorf("port %d PVID = %d, want 20", a, p.PVID)
			}
		}
		if !found {
			return fmt.Errorf("VLAN 20 not as configured: %v", got.VLANs)
		}
		return nil
	},
		mode("  mode = \"802.1q\"\n")+group+pvid(origPVID, modeAndGroup),
		mode("  mode = \"802.1q\"\n")+pvid(origPVID, modeOnly),
		mode(originalMode),
	)
}

// Import, drift and destroy semantics, on the LED and on a test port.
func TestScenarioLifecycle(t *testing.T) {
	s := openForScenario(t)
	ctx := context.Background()
	orig, err := s.Client.LED(ctx)
	if err != nil {
		t.Fatal(err)
	}
	led := func(on bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_led\" \"this\" {\n  enabled = %t\n}\n", on)
	}
	ledIs := func(want bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got, err := s.Client.LED(ctx)
			if err != nil || got != want {
				return fmt.Errorf("LED on the switch = %t, err %w, want %t", got, err, want)
			}
			return nil
		}
	}
	port := s.Target.TestPorts[0]
	ports, err := s.Client.Ports(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var origPort escp.Port
	for _, p := range ports {
		if int(p.ID) == port {
			origPort = p
		}
	}
	// The port is written with the values it has, so creating the resource changes nothing.
	portCfg := fmt.Sprintf("resource \"tplinkeasysmart_port\" \"this\" {\n  port         = %d\n  enabled      = %t\n  speed        = %q\n  flow_control = %t\n}\n",
		port, origPort.Enabled, string(origPort.ConfiguredSpeed), origPort.ConfiguredFlowControl)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: s.Factories(),
		Steps: []resource.TestStep{
			// Create, then import into a fresh state and compare every attribute.
			{Config: s.ProviderHCL() + led(orig) + portCfg},
			{Config: s.ProviderHCL() + led(orig) + portCfg, ResourceName: "tplinkeasysmart_led.this", ImportState: true, ImportStateId: "led", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "enabled"},
			{Config: s.ProviderHCL() + led(orig) + portCfg, ResourceName: "tplinkeasysmart_port.this", ImportState: true, ImportStateId: strconv.Itoa(port), ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "port", ImportStateVerifyIgnore: []string{"allow_disruptive"}},
			// Drift: someone changes the switch behind Terraform's back; the plan shows it, an apply fixes it.
			{PreConfig: func() {
				if err := s.Client.SetLED(ctx, !orig); err != nil {
					t.Fatal(err)
				}
			}, Config: s.ProviderHCL() + led(orig) + portCfg, PlanOnly: true, ExpectNonEmptyPlan: true},
			{Config: s.ProviderHCL() + led(orig) + portCfg, Check: ledIs(orig)},
			// Destroy only forgets: the switch keeps the last value.
			{Config: s.ProviderHCL() + led(!orig), Check: ledIs(!orig)},
			{Config: s.ProviderHCL(), Check: ledIs(!orig)},
			// Restore by configuration.
			{Config: s.ProviderHCL() + led(orig), Check: func(*terraform.State) error {
				if err := ledIs(orig)(nil); err != nil {
					return err
				}
				return s.restoredCheck(t)(nil)
			}},
		},
	})
}

// The configuration file hash is readable and changes when the switch is changed.
func TestScenarioConfigBackupDataSource(t *testing.T) {
	s := openForScenario(t)
	data := `data "tplinkeasysmart_config_backup" "this" {}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: s.Factories(),
		Steps: []resource.TestStep{{
			Config: s.ProviderHCL() + data,
			Check: resource.ComposeTestCheckFunc(
				resource.TestMatchResourceAttr("data.tplinkeasysmart_config_backup.this", "sha256", regexp.MustCompile(`^[0-9a-f]{64}$`)),
				resource.TestCheckResourceAttrSet("data.tplinkeasysmart_config_backup.this", "size"),
			),
		}},
	})
}

// managementIP is a tplinkeasysmart_management_ip resource for the given address triple.
func managementIP(ip, mask, gateway string) string {
	return fmt.Sprintf("resource \"tplinkeasysmart_management_ip\" \"this\" {\n  ip_address = %q\n  netmask    = %q\n  gateway    = %q\n}\n", ip, mask, gateway)
}

// writesUnchanged returns a check that the fake received no write since n; on hardware the
// readout comparison that closes every scenario is the only evidence, as there is no counter.
func (s *Session) writesUnchanged(n int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if s.Fake != nil {
			if got := s.Fake.WriteCount(); got != n {
				return fmt.Errorf("the switch received %d write(s), want none", got-n)
			}
		}
		return nil
	}
}

// The management address is managed with the address the switch already has: nothing is written,
// the resource imports, and the plan is clean. Moving the switch is not part of this scenario
// (see the live move in the sw03 target): the fake answers on loopback, where the client refuses
// to move it, and a move to a documentation address would aim packets at nothing.
func TestScenarioManagementAddressUnchanged(t *testing.T) {
	s := openForScenario(t)
	info, err := s.Client.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.ProviderHCL() + managementIP(info.IP.String(), info.Netmask.String(), info.Gateway.String())
	writes := 0
	if s.Fake != nil {
		writes = s.Fake.WriteCount()
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: cfg, Check: resource.ComposeTestCheckFunc(
			resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.this", "ip_address", info.IP.String()),
			resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.this", "id", "management_ip"),
			s.writesUnchanged(writes),
			s.restoredCheck(t))},
		{Config: cfg, ResourceName: "tplinkeasysmart_management_ip.this", ImportState: true, ImportStateVerify: true,
			ImportStateVerifyIgnore: []string{"allow_disruptive"}},
	}})
}

// A different mask is refused while allow_disruptive is false, before anything is written, and a
// mask that is not a prefix is refused at plan time.
func TestScenarioManagementAddressRefusals(t *testing.T) {
	s := openForScenario(t)
	info, err := s.Client.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other := "255.255.0.0"
	if info.Netmask.String() == other {
		other = "255.255.255.0"
	}
	writes := 0
	if s.Fake != nil {
		writes = s.Fake.WriteCount()
	}
	resource.Test(t, resource.TestCase{ProtoV6ProviderFactories: s.Factories(), Steps: []resource.TestStep{
		{Config: s.ProviderHCL() + managementIP(info.IP.String(), other, info.Gateway.String()),
			ExpectError: regexp.MustCompile(`Refusing to change the management address`)},
		{Config: s.ProviderHCL() + managementIP(info.IP.String(), "255.0.255.0", info.Gateway.String()),
			ExpectError: regexp.MustCompile(`contiguous prefix`)},
		{Config: s.ProviderHCL() + managementIP(info.IP.String(), info.Netmask.String(), info.Gateway.String()), Check: resource.ComposeTestCheckFunc(
			s.writesUnchanged(writes), s.restoredCheck(t))},
	}})
}
