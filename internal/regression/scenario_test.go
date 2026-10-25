package regression

import (
	"context"
	"fmt"
	"os"
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
	steps := []resource.TestStep{{Config: s.ProviderHCL() + change, Check: verifyChange}}
	for i, cfg := range restore {
		step := resource.TestStep{Config: s.ProviderHCL() + cfg}
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
		if p.Port == port {
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
			if p.Port == port && p.Priority != changed {
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
		if p.ID == port {
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
			if p.ID == port && p.ConfiguredSpeed != changed {
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
		if b.Port == port {
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
			if b.Port == port && (b.IngressKbps != 10240 || b.EgressKbps != 5120) {
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
		if p.Port == port {
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
			if p.Port == port && (!p.Multicast || !p.Broadcast || p.RateKbps != 1024) {
				return fmt.Errorf("port %d storm control = %+v, want multicast and broadcast at 1024", port, p)
			}
		}
		return nil
	})
}
