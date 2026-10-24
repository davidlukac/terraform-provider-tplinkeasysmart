package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// allow_disruptive is an instruction about the next change, not a setting of the switch. Adding it
// to a resource on a live port must not rewrite that port, and removing it must not be refused by
// the guard; a real change without it is still guarded. One case per guarded resource.
func TestAllowDisruptiveAloneNeverTouchesTheSwitch(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		fake    func() *fakeClient
		with    string // applied with the opt-in: a write to a used port
		without string // the same settings, opt-in removed
		changed string // a real change, no opt-in: the guard must refuse it
		refused string
	}{
		{"port", "tplinkeasysmart_port.p",
			func() *fakeClient { return &fakeClient{ports: testPorts()} },
			portConfig(4, "  flow_control = true\n  allow_disruptive = true\n"), portConfig(4, "  flow_control = true\n"),
			portConfig(4, "  flow_control = true\n  enabled = false\n"), `port 4 has a link up`},
		{"bandwidth_limit", "tplinkeasysmart_bandwidth_limit.b",
			func() *fakeClient { return &fakeClient{ports: testPorts(), bw: map[int][2]int{}} },
			bwConfig(4, "  ingress_kbps = 64\n  allow_disruptive = true\n"), bwConfig(4, "  ingress_kbps = 64\n"),
			bwConfig(4, "  ingress_kbps = 128\n"), `port 4 has a link up`},
		{"storm_limit", "tplinkeasysmart_storm_limit.s",
			func() *fakeClient { return &fakeClient{ports: testPorts(), storm: map[int]escp.PortStorm{}} },
			stormConfig(4, "  broadcast = true\n  rate_kbps = 64\n  allow_disruptive = true\n"), stormConfig(4, "  broadcast = true\n  rate_kbps = 64\n"),
			stormConfig(4, "  broadcast = true\n  rate_kbps = 128\n"), `port 4 has a link up`},
		{"port_mirror", "tplinkeasysmart_port_mirror.m",
			func() *fakeClient { return &fakeClient{ports: testPorts(), mirror: &escp.Mirror{}} },
			mirrorConfig("  enabled = true\n  mirror_port = 4\n  ingress_ports = [7]\n  allow_disruptive = true\n"),
			mirrorConfig("  enabled = true\n  mirror_port = 4\n  ingress_ports = [7]\n"),
			mirrorConfig("  enabled = true\n  mirror_port = 4\n  ingress_ports = [6, 7]\n"), `port 4 has a link up`},
		{"lag", "tplinkeasysmart_lag.l",
			func() *fakeClient { return &fakeClient{ports: lagPorts(), lags: map[int][]int{1: {1, 2}}} },
			lagConfig(1, "  ports = [3, 4]\n  allow_disruptive = true\n"), lagConfig(1, "  ports = [3, 4]\n"),
			lagConfig(1, "  ports = [2, 3, 4]\n"), `link up`},
		{"vlan_port_based_group", "tplinkeasysmart_vlan_port_based_group.v",
			func() *fakeClient { return &fakeClient{ports: lagPorts(), pvlans: map[int][]int{1: {1, 2, 4, 6, 7}}} },
			vlanGroupConfig(2, "  ports = [4]\n  allow_disruptive = true\n"), vlanGroupConfig(2, "  ports = [4]\n"),
			vlanGroupConfig(2, "  ports = [4, 6]\n"), `port 4 has a link up`},
		{"vlan_8021q_group", "tplinkeasysmart_vlan_8021q_group.v",
			func() *fakeClient { return newVLANFake("802.1q") },
			groupConfig8021Q(20, "  tagged_ports = [4]\n  allow_disruptive = true\n"), groupConfig8021Q(20, "  tagged_ports = [4]\n"),
			groupConfig8021Q(20, "  tagged_ports = [4, 5]\n"), `port 4 has a link up`},
		{"vlan_8021q_pvid", "tplinkeasysmart_vlan_8021q_pvid.p",
			func() *fakeClient { return newPVIDFake() },
			pvidConfig(4, 20, "  allow_disruptive = true\n"), pvidConfig(4, 20, ""),
			pvidConfig(4, 1, ""), `port 4 has a link up`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := tc.fake()
			var written int
			sets := func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.sets }
			unchanged := func(*terraformState) error {
				if n := sets(); n != written {
					return fmt.Errorf("the switch received %d write(s) after the opt-in alone changed", n-written)
				}
				return nil
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps: []resource.TestStep{
					{Config: tc.with, Check: func(*terraformState) error {
						if written = sets(); written == 0 {
							return fmt.Errorf("the opt-in change did not reach the switch")
						}
						return nil
					}},
					// the opt-in is removed: the guard must not refuse, and nothing may be written
					{Config: tc.without, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(tc.addr, "allow_disruptive", "false"), unchanged)},
					// and added again: nothing may be rewritten
					{Config: tc.with, Check: resource.ComposeTestCheckFunc(resource.TestCheckResourceAttr(tc.addr, "allow_disruptive", "true"), unchanged)},
					{Config: tc.without, Check: unchanged},
					// a real change without the opt-in is still guarded
					{Config: tc.changed, ExpectError: regexp.MustCompile(tc.refused)},
					// the opt-in again, so that the destroy at the end of the test is allowed (and writes nothing new here)
					{Config: tc.with, Check: unchanged, PreConfig: func() {
						if n := sets(); n != written {
							t.Errorf("the refused change wrote to the switch: %d write(s) beyond the first", n-written)
						}
					}},
				},
			})
		})
	}
}

func TestPlanWithOnlyTheFlagChanged(t *testing.T) {
	obj := tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"port": tftypes.Number, "actual_speed": tftypes.String, "allow_disruptive": tftypes.Bool, "ports": tftypes.Set{ElementType: tftypes.Number}}}
	set := func(v ...int) tftypes.Value {
		var els []tftypes.Value
		for _, x := range v {
			els = append(els, tftypes.NewValue(tftypes.Number, x))
		}
		return tftypes.NewValue(tftypes.Set{ElementType: tftypes.Number}, els)
	}
	mk := func(port int, speed any, flag bool, ports tftypes.Value) tftypes.Value {
		return tftypes.NewValue(obj, map[string]tftypes.Value{"port": tftypes.NewValue(tftypes.Number, port),
			"actual_speed": tftypes.NewValue(tftypes.String, speed), "allow_disruptive": tftypes.NewValue(tftypes.Bool, flag), "ports": ports})
	}
	prior := mk(4, "1000Mbps-full", true, set(1, 2))

	t.Run("the flag alone, in both directions", func(t *testing.T) {
		got, ok := planWithOnlyTheFlagChanged(mk(4, "1000Mbps-full", false, set(1, 2)), prior)
		if !ok || !got.Equal(mk(4, "1000Mbps-full", false, set(1, 2))) {
			t.Fatalf("ok=%v got=%v", ok, got)
		}
		if _, ok := planWithOnlyTheFlagChanged(prior, mk(4, "1000Mbps-full", false, set(1, 2))); !ok {
			t.Fatal("adding the flag alone must be recognised too")
		}
	})
	t.Run("a computed attribute the plan cannot decide keeps its prior value", func(t *testing.T) {
		got, ok := planWithOnlyTheFlagChanged(mk(4, tftypes.UnknownValue, false, set(1, 2)), prior)
		if !ok || !got.Equal(mk(4, "1000Mbps-full", false, set(1, 2))) {
			t.Fatalf("ok=%v got=%v", ok, got)
		}
	})
	t.Run("any other difference is a real change", func(t *testing.T) {
		for name, plan := range map[string]tftypes.Value{
			"a number": mk(5, "1000Mbps-full", false, set(1, 2)),
			"a set":    mk(4, "1000Mbps-full", false, set(1, 2, 3)),
			"a string": mk(4, "100Mbps-full", false, set(1, 2)),
		} {
			if _, ok := planWithOnlyTheFlagChanged(plan, prior); ok {
				t.Errorf("%s changed together with the flag, yet it was taken for the flag alone", name)
			}
		}
	})
	t.Run("no prior state, or a different shape, is not an update of the flag", func(t *testing.T) {
		if _, ok := planWithOnlyTheFlagChanged(prior, tftypes.NewValue(obj, nil)); ok {
			t.Error("a null prior state")
		}
		if _, ok := planWithOnlyTheFlagChanged(prior, tftypes.NewValue(tftypes.String, "x")); ok {
			t.Error("a prior state that is no object")
		}
		if _, ok := planWithOnlyTheFlagChanged(tftypes.NewValue(tftypes.String, "x"), prior); ok {
			t.Error("a plan that is no object")
		}
		other := tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"port": tftypes.Number, "allow_disruptive": tftypes.Bool}},
			map[string]tftypes.Value{"port": tftypes.NewValue(tftypes.Number, 4), "allow_disruptive": tftypes.NewValue(tftypes.Bool, true)})
		if _, ok := planWithOnlyTheFlagChanged(prior, other); ok {
			t.Error("an attribute the prior state does not have")
		}
	})
}
