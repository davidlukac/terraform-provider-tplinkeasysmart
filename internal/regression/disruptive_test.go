package regression

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// The disruptive scenarios make real changes to ports that carry a device, behind allow_disruptive:
// a speed change, a port taken down, a link aggregation group, a mirror destination and a
// port-based VLAN group. They are the changes the guards exist to stop, so they run on the fake
// always and on hardware only when the run opts in with TPLINK_REGRESSION_DISRUPTIVE_PORTS (the
// device ports they may disturb; never the uplink). Each ends with the readout of the whole
// switch identical to the first one, restored by explicit configuration.

// disruptivePorts returns the device ports the run may disturb, or skips the scenario.
func (s *Session) disruptivePorts(t *testing.T) []int {
	t.Helper()
	if len(s.Target.DisruptivePorts) == 0 {
		t.Skip("set TPLINK_REGRESSION_DISRUPTIVE_PORTS to the device ports this scenario may disturb")
	}
	return s.Target.DisruptivePorts
}

// portOf reads one port from the switch.
func (s *Session) portOf(t *testing.T, id int) escp.Port {
	t.Helper()
	ports, err := s.Client.Ports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range ports {
		if int(p.ID) == id {
			return p
		}
	}
	t.Fatalf("the switch has no port %d", id)
	return escp.Port{}
}

// portConfig is a port resource with allow_disruptive, which is needed to change a port in use.
func portConfig(port int, enabled bool, speed escp.Speed, flow bool) string {
	return fmt.Sprintf("resource \"tplinkeasysmart_port\" \"this\" {\n  port             = %d\n  enabled          = %t\n  speed            = %q\n  flow_control     = %t\n  allow_disruptive = true\n}\n", port, enabled, string(speed), flow)
}

// A speed change on a port with a device behind it.
func TestScenarioDisruptivePortSpeed(t *testing.T) {
	s := openForScenario(t)
	port := s.disruptivePorts(t)[0]
	orig := s.portOf(t, port)
	changed := escp.Speed100Full
	if orig.ConfiguredSpeed == changed {
		changed = escp.SpeedAuto
	}
	if orig.ConfiguredFlowControl {
		t.Skip("flow control is on; a fixed speed would be refused and this scenario does not turn it off")
	}
	s.scenario(t, portConfig(port, orig.Enabled, changed, false), portConfig(port, orig.Enabled, orig.ConfiguredSpeed, orig.ConfiguredFlowControl),
		func(*terraform.State) error {
			if got := s.portOf(t, port); got.ConfiguredSpeed != changed {
				return fmt.Errorf("port %d speed = %s, want %s", port, got.ConfiguredSpeed, changed)
			}
			return nil
		})
}

// A port taken down and brought back: the link goes away and the device behind it loses the network.
func TestScenarioDisruptivePortDisable(t *testing.T) {
	s := openForScenario(t)
	port := s.disruptivePorts(t)[0]
	orig := s.portOf(t, port)
	if !orig.Enabled {
		t.Skipf("port %d is already disabled", port)
	}
	s.scenario(t, portConfig(port, false, orig.ConfiguredSpeed, orig.ConfiguredFlowControl), portConfig(port, true, orig.ConfiguredSpeed, orig.ConfiguredFlowControl),
		func(*terraform.State) error {
			if got := s.portOf(t, port); got.Enabled {
				return fmt.Errorf("port %d is still enabled on the switch", port)
			}
			return nil
		})
}

// A link aggregation group made of two ports that both have a link. The switch checks its own member
// rules (the same speed, flow control and state), so the pair is chosen from the device ports with
// the same link speed and in the same group range (group 1 takes ports 1-4, group 2 ports 5-8).
func TestScenarioDisruptiveLAG(t *testing.T) {
	s := openForScenario(t)
	var a, b, group int
	for _, x := range s.disruptivePorts(t) {
		for _, y := range s.disruptivePorts(t) {
			px, py := s.portOf(t, x), s.portOf(t, y)
			if x < y && px.ActualSpeed == py.ActualSpeed && px.ActualSpeed != escp.SpeedLinkDown && px.ConfiguredSpeed == py.ConfiguredSpeed &&
				(x >= 5) == (y >= 5) && x != 1 {
				a, b, group = x, y, 1
				if x >= 5 {
					group = 2
				}
			}
		}
	}
	if group == 0 {
		t.Skip("no two disruptive ports with the same link speed in one group range")
	}
	cfg := fmt.Sprintf("resource \"tplinkeasysmart_lag\" \"this\" {\n  group            = %d\n  ports            = %s\n  allow_disruptive = true\n}\n", group, intSet(a, b))
	s.scenario(t, cfg, "", func(*terraform.State) error {
		got, err := s.Client.LAGs(context.Background())
		if err != nil {
			return err
		}
		for _, l := range got {
			if l.ID == group && l.Ports == escp.MustPortSet(escp.PortID(a), escp.PortID(b)) {
				return nil
			}
		}
		return fmt.Errorf("LAG %d not %v on the switch: %v", group, []int{a, b}, got)
	})
}

// A mirror whose monitoring port is a port with a device: that device receives copies of the source
// port's traffic.
func TestScenarioDisruptiveMirrorDestination(t *testing.T) {
	s := openForScenario(t)
	dest, src := s.disruptivePorts(t)[0], s.Target.TestPorts[0]
	cfg := func(body string) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_port_mirror\" \"this\" {\n%s  allow_disruptive = true\n}\n", body)
	}
	s.scenario(t, cfg(fmt.Sprintf("  enabled       = true\n  mirror_port   = %d\n  ingress_ports = %s\n", dest, intSet(src))), cfg("  enabled = false\n"),
		func(*terraform.State) error {
			got, err := s.Client.PortMirror(context.Background())
			if err != nil || !got.Enabled || int(got.MirrorPort) != dest || got.IngressPorts != escp.MustPortSet(escp.PortID(src)) {
				return fmt.Errorf("mirror on the switch = %+v, err %w, want %d mirrored to %d", got, err, src, dest)
			}
			return nil
		})
}

// A port-based VLAN group that takes a device port out of VLAN 1: the device is cut off from the
// uplink until the group is removed, which puts the port back (membership is exclusive).
func TestScenarioDisruptiveVLANGroup(t *testing.T) {
	s := openForScenario(t)
	vlan, err := s.Client.PortBasedVLAN(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !vlan.Enabled {
		t.Skip("the switch is not in port-based VLAN mode")
	}
	device, free := s.disruptivePorts(t)[0], s.Target.TestPorts[0]
	cfg := fmt.Sprintf("resource \"tplinkeasysmart_vlan_port_based_group\" \"this\" {\n  vlan             = 2\n  ports            = %s\n  allow_disruptive = true\n}\n", intSet(free, device))
	s.scenario(t, cfg, "", func(*terraform.State) error {
		got, err := s.Client.PortBasedVLAN(context.Background())
		if err != nil {
			return err
		}
		for _, v := range got.VLANs {
			if v.ID == 2 && v.Ports == escp.MustPortSet(escp.PortID(free), escp.PortID(device)) {
				return nil
			}
		}
		return fmt.Errorf("VLAN 2 not %v on the switch: %v", []int{free, device}, got.VLANs)
	})
}
