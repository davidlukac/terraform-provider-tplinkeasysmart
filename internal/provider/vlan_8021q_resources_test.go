package provider

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// vlanPorts is a switch where 1 and 2 are the LAG to the router, 4 and 8 carry
// a link, and 3, 5, 6 and 7 are free.
func vlanPorts() []escp.Port {
	down, up := escp.SpeedLinkDown, escp.Speed1000Full
	port := func(id int, lag int, speed escp.Speed) escp.Port {
		return escp.Port{ID: escp.PortID(id), Enabled: true, LAG: lag, ConfiguredSpeed: "auto", ActualSpeed: speed}
	}
	return []escp.Port{port(1, 1, up), port(2, 1, up), port(3, 0, down), port(4, 0, up), port(5, 0, down), port(6, 0, down), port(7, 0, down), port(8, 0, up)}
}

func newVLANFake(mode string) *fakeClient {
	return &fakeClient{ports: vlanPorts(), vlans: newFakeVLANs(mode)}
}

func (f *fakeClient) vlanState() *fakeVLANs { f.mu.Lock(); defer f.mu.Unlock(); return f.vlans }

// withVLAN adds a VLAN to the fake's 802.1Q table behind the provider's back (the web UI, another run).
func (f *fakeClient) withVLAN(v escp.VLAN8021Q) {
	f.mutate(func(f *fakeClient) { f.vlans.vlans[v.ID] = v })
}

func modeConfig(body string) string {
	return providerBlock + "resource \"tplinkeasysmart_vlan_mode\" \"m\" {\n" + body + "}\n"
}

func groupConfig8021Q(id int, body string) string {
	return providerBlock + fmt.Sprintf("resource \"tplinkeasysmart_vlan_8021q_group\" \"v\" {\n  vlan = %d\n%s}\n", id, body)
}

func pvidConfig(port, pvid int, extra string) string {
	return providerBlock + fmt.Sprintf("resource \"tplinkeasysmart_vlan_8021q_pvid\" \"p\" {\n  port = %d\n  pvid = %d\n%s}\n", port, pvid, extra)
}

// ---- tplinkeasysmart_vlan_mode ----

func TestAccVLANModeSwitchesToDot1QWhenAllowed(t *testing.T) {
	f := newVLANFake("port-based")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{
				Config: modeConfig("  mode = \"802.1q\"\n  allow_disruptive = true\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_mode.m", "mode", "802.1q"),
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_mode.m", "id", "vlan_mode"),
					func(*terraformState) error {
						if v := f.vlanState(); !v.enabled || v.portBased {
							return fmt.Errorf("switch is not in 802.1Q mode: %+v", v)
						}
						return nil
					},
				),
			},
			{ResourceName: "tplinkeasysmart_vlan_mode.m", ImportState: true, ImportStateId: "vlan_mode", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"allow_disruptive"}},
		},
	})
	if f.sets != 1 {
		t.Fatalf("exactly one mode write expected, got %d", f.sets)
	}
}

func TestAccVLANModeRefusesToSwitchWithoutAllowDisruptive(t *testing.T) {
	f := newVLANFake("port-based")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: modeConfig("  mode = \"802.1q\"\n"), ExpectError: regexp.MustCompile(`Refusing to change the VLAN mode`)},
			{Config: modeConfig("  mode = \"mtu\"\n  mtu_uplink_port = 8\n"), ExpectError: regexp.MustCompile(`switch is in VLAN mode "port-based"`)},
		},
	})
	if f.sets != 0 || !f.vlanState().portBased {
		t.Fatalf("a refused change must not write: sets=%d state=%+v", f.sets, f.vlanState())
	}
}

func TestAccVLANModeNeedsNoWriteWhenAlreadyActive(t *testing.T) {
	f := newVLANFake("port-based")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps:                    []resource.TestStep{{Config: modeConfig("  mode = \"port-based\"\n"), Check: resource.TestCheckResourceAttr("tplinkeasysmart_vlan_mode.m", "mode", "port-based")}},
	})
	if f.sets != 0 {
		t.Fatalf("the mode is already active, expected no write, got %d", f.sets)
	}
}

func TestAccVLANModeMTUNeedsAnUplinkPortAndOnlyThen(t *testing.T) {
	f := newVLANFake("port-based")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: modeConfig("  mode = \"mtu\"\n"), ExpectError: regexp.MustCompile(`requires mtu_uplink_port`)},
			{Config: modeConfig("  mode = \"802.1q\"\n  mtu_uplink_port = 8\n"), ExpectError: regexp.MustCompile(`only used with mode = "mtu"`)},
			{Config: modeConfig("  mode = \"mtu\"\n  mtu_uplink_port = 99\n"), ExpectError: regexp.MustCompile(`must be between 1 and 32`)},
			{Config: modeConfig("  mode = \"vlan\"\n"), ExpectError: regexp.MustCompile(`must be one of`)},
			{
				Config: modeConfig("  mode = \"mtu\"\n  mtu_uplink_port = 8\n  allow_disruptive = true\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_mode.m", "mode", "mtu"),
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_mode.m", "mtu_uplink_port", "8"),
				),
			},
			// a different uplink port is a change as well, and is guarded like the mode itself
			{Config: modeConfig("  mode = \"mtu\"\n  mtu_uplink_port = 7\n"), ExpectError: regexp.MustCompile(`Refusing to change the VLAN mode`)},
		},
	})
	if f.sets != 1 {
		t.Fatalf("exactly one write expected, got %d", f.sets)
	}
}

func TestAccVLANModeDriftIsDetected(t *testing.T) {
	f := newVLANFake("port-based")
	cfg := modeConfig("  mode = \"port-based\"\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:          func() { f.mutate(func(f *fakeClient) { f.vlans.enable8021Q() }) }, // someone switches the mode in the web UI
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// a switch with no mode on reports "none" and the plan shows the difference
			{
				PreConfig:          func() { f.mutate(func(f *fakeClient) { f.vlans = newFakeVLANs("") }) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccVLANModeReportsFailures(t *testing.T) {
	cfg := modeConfig("  mode = \"802.1q\"\n  allow_disruptive = true\n")
	cases := []struct {
		name  string
		setup func(w *faultyClient)
		want  string
	}{
		// the library reads the three flags (its own tests make each of them fail)
		{"mode read fails", func(w *faultyClient) { w.failAlways("VLANMode") }, `Reading the VLAN mode failed`},
		{"write fails", func(w *faultyClient) { w.failAlways("SetVLANMode") }, `Setting the VLAN mode failed`},
		{"write is accepted but ignored", func(w *faultyClient) { w.ignoreOnly("SetVLANMode") }, `did not change as requested`},
		{"read back fails", func(w *faultyClient) { w.failCalls("VLANMode", 2, 0) }, `did not change as requested`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newFaulty(newVLANFake("port-based"))
			tc.setup(w)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(w),
				Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}

func TestAccVLANModeImportRejectsOtherIDs(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(newVLANFake("port-based")),
		Steps: []resource.TestStep{
			{Config: modeConfig("  mode = \"port-based\"\n")},
			{ResourceName: "tplinkeasysmart_vlan_mode.m", ImportState: true, ImportStateId: "mode", ExpectError: regexp.MustCompile(`the only valid id is "vlan_mode"`)},
		},
	})
}

// ---- tplinkeasysmart_vlan_8021q_group ----

func TestAccVLAN8021QGroupLifecycleOnFreePorts(t *testing.T) {
	f := newVLANFake("802.1q")
	cfg := func(body string) string { return groupConfig8021Q(20, body) }
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{
				Config: cfg("  name = \"media\"\n  tagged_ports = [5]\n  untagged_ports = [6, 7]\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_8021q_group.v", "name", "media"),
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_8021q_group.v", "tagged_ports.#", "1"),
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_8021q_group.v", "untagged_ports.#", "2"),
				),
			},
			// an update replaces the membership; the name falls back to its default
			{
				Config: cfg("  untagged_ports = [6]\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_8021q_group.v", "name", ""),
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_8021q_group.v", "tagged_ports.#", "0"),
					func(*terraformState) error {
						got := f.vlanState().vlans[20]
						if !got.Tagged.IsEmpty() || got.Untagged != escp.MustPortSet(6) {
							return fmt.Errorf("switch has %+v", got)
						}
						return nil
					},
				),
			},
			{ResourceName: "tplinkeasysmart_vlan_8021q_group.v", ImportState: true, ImportStateId: "20", ImportStateVerify: true,
				ImportStateVerifyIdentifierAttribute: "vlan"},
		},
	})
	if _, still := f.vlanState().vlans[20]; still {
		t.Fatal("VLAN 20 must be removed on destroy")
	}
	if len(f.vlanState().vlans) != 1 {
		t.Fatalf("only the default VLAN may be left: %+v", f.vlanState().vlans)
	}
}

func TestAccVLAN8021QGroupRefusesOtherModes(t *testing.T) {
	for _, mode := range []string{"port-based", "mtu", ""} {
		t.Run("mode "+mode, func(t *testing.T) {
			t.Parallel()
			f := newVLANFake(mode)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps: []resource.TestStep{{Config: groupConfig8021Q(20, "  untagged_ports = [6]\n"),
					ExpectError: regexp.MustCompile(`802.1Q VLAN mode is not active`)}},
			})
			if f.sets != 0 {
				t.Fatalf("no write may happen outside 802.1Q mode, got %d", f.sets)
			}
		})
	}
}

func TestAccVLAN8021QGroupGuardBlocksUsedPorts(t *testing.T) {
	f := newVLANFake("802.1q")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: groupConfig8021Q(20, "  untagged_ports = [4]\n"), ExpectError: regexp.MustCompile(`port 4 has a link up`)},
			{Config: groupConfig8021Q(20, "  tagged_ports = [1]\n"), ExpectError: regexp.MustCompile(`port 1 has a link up`)},
			{Config: groupConfig8021Q(20, "  untagged_ports = [6, 8]\n"), ExpectError: regexp.MustCompile(`port 8 has a link up`)},
			// VLAN 1 holds every port, linked ones included
			{Config: groupConfig8021Q(1, "  name = \"Default\"\n  untagged_ports = [3, 5]\n"), ExpectError: regexp.MustCompile(`has a link up|member of LAG`)},
		},
	})
	if f.sets != 0 {
		t.Fatalf("the guard must prevent any write, got %d", f.sets)
	}
}

func TestAccVLAN8021QGroupGuardOverride(t *testing.T) {
	f := newVLANFake("802.1q")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: groupConfig8021Q(20, "  tagged_ports = [4]\n  allow_disruptive = true\n"),
			Check:  func(*terraformState) error { return expectVLANPorts(f, 20, escp.MustPortSet(4), 0) },
		}},
	})
}

func expectVLANPorts(f *fakeClient, id int, tagged, untagged escp.PortSet) error {
	got, ok := f.vlanState().vlans[id]
	if !ok {
		return fmt.Errorf("VLAN %d is not on the switch", id)
	}
	if got.Tagged != tagged || got.Untagged != untagged {
		return fmt.Errorf("VLAN %d: tagged %s untagged %s, want %s and %s", id, got.Tagged, got.Untagged, tagged, untagged)
	}
	return nil
}

func TestAccVLAN8021QGroupNeverLeavesAPortInNoVLAN(t *testing.T) {
	// ports 5, 6 and 7 are members of VLAN 20 only; the default VLAN has the others
	f := newVLANFake("802.1q")
	f.mutate(func(f *fakeClient) {
		f.vlans.vlans[1] = escp.VLAN8021Q{ID: 1, Name: "Default", Untagged: escp.MustPortSet(1, 2, 3, 4, 8), Tagged: 0}
	})
	cfg := groupConfig8021Q(20, "  untagged_ports = [5, 6]\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			// creating VLAN 20 on ports that are in no VLAN yet is fine: it gives them one
			{Config: cfg},
			// shrinking it would strand port 6
			{Config: groupConfig8021Q(20, "  untagged_ports = [5]\n"), ExpectError: regexp.MustCompile(`(?s)leave\s+port\(s\)\s+6\s+in\s+no\s+VLAN`)},
			{Config: groupConfig8021Q(20, "  untagged_ports = [5]\n  allow_disruptive = true\n")},
			// destroying it would strand port 5; the override is part of the state
			{Config: providerBlock, Destroy: false},
		},
	})
	if _, still := f.vlanState().vlans[20]; still {
		t.Fatal("VLAN 20 should be gone after the destroy")
	}
}

func TestAccVLAN8021QGroupDestroyIsGuarded(t *testing.T) {
	f := newVLANFake("802.1q")
	f.mutate(func(f *fakeClient) {
		f.vlans.vlans[1] = escp.VLAN8021Q{ID: 1, Name: "Default", Untagged: escp.MustPortSet(1, 2, 3, 4, 8), Tagged: 0}
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: groupConfig8021Q(20, "  untagged_ports = [5]\n")},
			// remove it from the configuration: the plan wants to destroy VLAN 20, which would strand port 5
			{Config: providerBlock, ExpectError: regexp.MustCompile(`Refusing to remove the 802.1Q VLAN`)},
			// destroy it for real at the end with the override recorded in state
			{Config: groupConfig8021Q(20, "  untagged_ports = [5]\n  allow_disruptive = true\n")},
		},
	})
	if _, still := f.vlanState().vlans[20]; still {
		t.Fatal("the final destroy must remove VLAN 20")
	}
}

func TestAccVLAN8021QGroupValidatesConfiguration(t *testing.T) {
	cases := []struct{ name, cfg, want string }{
		{"no ports", groupConfig8021Q(20, "  name = \"x\"\n"), `needs at least one tagged or untagged port`},
		{"port both tagged and untagged", groupConfig8021Q(20, "  tagged_ports = [5]\n  untagged_ports = [5, 6]\n"), `port 5 is in tagged_ports and untagged_ports`},
		{"id zero", groupConfig8021Q(0, "  untagged_ports = [5]\n"), `must be between 1 and 4094`},
		{"id too big", groupConfig8021Q(4095, "  untagged_ports = [5]\n"), `must be between 1 and 4094`},
		{"name too long", groupConfig8021Q(20, "  name = \"elevenchars\"\n  untagged_ports = [5]\n"), `string length must be at most 10`},
		{"name with a space", groupConfig8021Q(20, "  name = \"a b\"\n  untagged_ports = [5]\n"), `only letters, digits, hyphens and underscores`},
		{"port out of range", groupConfig8021Q(20, "  untagged_ports = [33]\n"), `must be between 1 and 32`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newVLANFake("802.1q")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if f.sets != 0 {
				t.Fatalf("an invalid configuration must not write, got %d", f.sets)
			}
		})
	}
}

func TestAccVLAN8021QGroupDriftIsDetected(t *testing.T) {
	f := newVLANFake("802.1q")
	cfg := groupConfig8021Q(20, "  name = \"media\"\n  untagged_ports = [5, 6]\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					f.withVLAN(escp.VLAN8021Q{ID: 20, Name: "media", Untagged: escp.MustPortSet(5), Tagged: escp.MustPortSet(6)})
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg, Check: func(*terraformState) error { return expectVLANPorts(f, 20, 0, escp.MustPortSet(5, 6)) }},
			// the VLAN vanishes behind our back: it is planned for creation again
			{
				PreConfig:          func() { f.mutate(func(f *fakeClient) { delete(f.vlans.vlans, 20) }) },
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccVLAN8021QGroupReportsFailures(t *testing.T) {
	cfg := groupConfig8021Q(20, "  untagged_ports = [5]\n")
	cases := []struct {
		name  string
		setup func(w *faultyClient)
		want  string
	}{
		{"table read fails", func(w *faultyClient) { w.failAlways("Dot1Q") }, `Reading the 802.1Q VLANs failed`},
		{"ports read fails", func(w *faultyClient) { w.failAlways("Ports") }, `cannot check whether the ports are in use|Refusing to change the 802.1Q VLAN`},
		{"write fails", func(w *faultyClient) { w.failAlways("SetVLAN8021Q") }, `Setting the 802.1Q VLAN failed`},
		{"write is accepted but ignored", func(w *faultyClient) { w.ignoreOnly("SetVLAN8021Q") }, `did not change as requested`},
		{"read back fails", func(w *faultyClient) { w.failCalls("Dot1Q", 2, 0) }, `did not change as requested`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newFaulty(newVLANFake("802.1q"))
			tc.setup(w)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(w),
				Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}

func TestAccVLAN8021QGroupDeleteFailures(t *testing.T) {
	cfg := groupConfig8021Q(20, "  untagged_ports = [5]\n")
	for name, want := range map[string]string{"DeleteVLAN8021Q": `Removing the 802.1Q VLAN failed`} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newFaulty(newVLANFake("802.1q"))
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(w),
				Steps: []resource.TestStep{
					{Config: cfg},
					{PreConfig: func() { w.failAlways(name) }, Config: providerBlock, ExpectError: regexp.MustCompile(want)},
					{PreConfig: w.heal, Config: providerBlock},
				},
			})
		})
	}
	t.Run("the delete is accepted but ignored", func(t *testing.T) {
		t.Parallel()
		w := newFaulty(newVLANFake("802.1q"))
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(w),
			Steps: []resource.TestStep{
				{Config: cfg},
				{PreConfig: func() { w.ignoreOnly("DeleteVLAN8021Q") }, Config: providerBlock, ExpectError: regexp.MustCompile(`The 802.1Q VLAN was not removed`)},
				{PreConfig: w.heal, Config: providerBlock},
			},
		})
	})
	t.Run("the table cannot be read at destroy", func(t *testing.T) {
		t.Parallel()
		w := newFaulty(newVLANFake("802.1q"))
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(w),
			Steps: []resource.TestStep{
				{Config: cfg},
				// the refresh before the destroy is the first read; the destroy itself is the second
				{PreConfig: func() { w.heal(); w.failCalls("Dot1Q", 2, 2) }, Config: providerBlock, ExpectError: regexp.MustCompile(`Reading the 802.1Q VLANs failed`)},
				{PreConfig: w.heal, Config: providerBlock},
			},
		})
	})
	t.Run("the table cannot be read while refreshing", func(t *testing.T) {
		t.Parallel()
		w := newFaulty(newVLANFake("802.1q"))
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(w),
			Steps: []resource.TestStep{
				{Config: cfg},
				{PreConfig: func() { w.heal(); w.failAlways("Dot1Q") }, Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`Reading the 802.1Q VLANs failed`)},
				{PreConfig: w.heal, Config: providerBlock},
			},
		})
	})
}

func TestAccVLAN8021QGroupDestroyOfAVLANThatIsAlreadyGone(t *testing.T) {
	f := newVLANFake("802.1q")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: groupConfig8021Q(20, "  untagged_ports = [5]\n")},
			{PreConfig: func() { f.mutate(func(f *fakeClient) { delete(f.vlans.vlans, 20) }) }, Config: providerBlock},
		},
	})
	if f.sets != 1 {
		t.Fatalf("only the create may write; the destroy finds the VLAN gone, got %d writes", f.sets)
	}
}

func TestAccVLAN8021QGroupImportRejectsBadIDs(t *testing.T) {
	f := newVLANFake("802.1q")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: groupConfig8021Q(20, "  untagged_ports = [5]\n")},
			{ResourceName: "tplinkeasysmart_vlan_8021q_group.v", ImportState: true, ImportStateId: "twenty", ExpectError: regexp.MustCompile(`expected a VLAN id`)},
		},
	})
}

func TestOrphanedPorts(t *testing.T) {
	table := []escp.VLAN8021Q{
		{ID: 1, Untagged: escp.MustPortSet(1, 2, 3)},
		{ID: 20, Untagged: escp.MustPortSet(5), Tagged: escp.MustPortSet(6)},
		{ID: 30, Untagged: escp.MustPortSet(6, 7)},
	}
	tests := []struct {
		name       string
		id         int
		newMembers escp.PortSet
		affected   escp.PortSet
		want       escp.PortSet
	}{
		{"deleting a VLAN whose ports have another", 30, 0, escp.MustPortSet(6, 7), escp.MustPortSet(7)},
		{"moving a port out of a VLAN that was its only one", 20, escp.MustPortSet(6), escp.MustPortSet(5, 6), escp.MustPortSet(5)},
		{"every affected port stays covered", 20, escp.MustPortSet(5, 6), escp.MustPortSet(5, 6), 0},
		{"a new VLAN covers its own ports", 40, escp.MustPortSet(4), escp.MustPortSet(4), 0},
		{"orphans are all named", 20, 0, escp.MustPortSet(6, 5, 4), escp.MustPortSet(4, 5)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := orphanedPorts(table, tt.id, tt.newMembers, tt.affected); got != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

// A VLAN is a value: port order cannot matter because a set has none, and every field counts.
func TestVLANsCompareByIdNameAndPortSets(t *testing.T) {
	a := escp.VLAN8021Q{ID: 2, Name: "x", Tagged: escp.MustPortSet(6, 5), Untagged: escp.MustPortSet(1)}
	if a != (escp.VLAN8021Q{ID: 2, Name: "x", Tagged: escp.MustPortSet(5, 6), Untagged: escp.MustPortSet(1)}) {
		t.Fatal("port order must not matter")
	}
	for name, b := range map[string]escp.VLAN8021Q{
		"id":       {ID: 3, Name: "x", Tagged: escp.MustPortSet(5, 6), Untagged: escp.MustPortSet(1)},
		"name":     {ID: 2, Name: "y", Tagged: escp.MustPortSet(5, 6), Untagged: escp.MustPortSet(1)},
		"tagged":   {ID: 2, Name: "x", Tagged: escp.MustPortSet(5), Untagged: escp.MustPortSet(1)},
		"untagged": {ID: 2, Name: "x", Tagged: escp.MustPortSet(5, 6), Untagged: escp.MustPortSet(2)},
	} {
		if a == b {
			t.Fatalf("a different %s must not compare equal", name)
		}
	}
}

// ---- tplinkeasysmart_vlan_8021q_pvid ----

func newPVIDFake() *fakeClient {
	f := newVLANFake("802.1q")
	f.withVLAN(escp.VLAN8021Q{ID: 20, Name: "media", Untagged: escp.MustPortSet(5, 6), Tagged: 0})
	return f
}

func expectPVID(f *fakeClient, port escp.PortID, want int) error {
	if got := f.vlanState().pvids[port]; got != want {
		return fmt.Errorf("port %d has PVID %d, want %d", port, got, want)
	}
	return nil
}

func TestAccVLAN8021QPVIDLifecycleOnAFreePort(t *testing.T) {
	f := newPVIDFake()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{
				Config: pvidConfig(5, 20, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_vlan_8021q_pvid.p", "pvid", "20"),
					func(*terraformState) error { return expectPVID(f, 5, 20) },
				),
			},
			{Config: pvidConfig(5, 1, ""), Check: func(*terraformState) error { return expectPVID(f, 5, 1) }},
			{Config: pvidConfig(5, 20, "")},
			{ResourceName: "tplinkeasysmart_vlan_8021q_pvid.p", ImportState: true, ImportStateId: "5", ImportStateVerify: true, ImportStateVerifyIdentifierAttribute: "port"},
		},
	})
	if err := expectPVID(f, 5, 1); err != nil {
		t.Fatalf("destroy must put the PVID back to 1: %v", err)
	}
}

func TestAccVLAN8021QPVIDRefusals(t *testing.T) {
	t.Run("the VLAN does not exist", func(t *testing.T) {
		t.Parallel()
		f := newPVIDFake()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: pvidConfig(5, 99, ""), ExpectError: regexp.MustCompile(`VLAN 99 is not defined on the switch`)}},
		})
		if f.sets != 0 {
			t.Fatalf("expected no PVID write, got %d writes", f.sets)
		}
	})
	t.Run("another VLAN mode is active", func(t *testing.T) {
		t.Parallel()
		f := newVLANFake("port-based")
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: pvidConfig(5, 1, ""), ExpectError: regexp.MustCompile(`802.1Q VLAN mode is not active`)}},
		})
		if f.sets != 0 {
			t.Fatalf("expected no write, got %d", f.sets)
		}
	})
	t.Run("the port carries a link or is in a LAG", func(t *testing.T) {
		t.Parallel()
		f := newPVIDFake()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps: []resource.TestStep{
				{Config: pvidConfig(4, 20, ""), ExpectError: regexp.MustCompile(`port 4 has a link up`)},
				{Config: pvidConfig(1, 20, ""), ExpectError: regexp.MustCompile(`port 1 has a link up`)},
			},
		})
		if err := expectPVID(f, 4, 1); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("the override lets a linked port change", func(t *testing.T) {
		t.Parallel()
		f := newPVIDFake()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: pvidConfig(4, 20, "  allow_disruptive = true\n"), Check: func(*terraformState) error { return expectPVID(f, 4, 20) }}},
		})
	})
}

func TestAccVLAN8021QPVIDValidatesConfiguration(t *testing.T) {
	cases := []struct{ name, cfg, want string }{
		{"pvid zero", pvidConfig(5, 0, ""), `must be between 1 and 4094`},
		{"pvid too big", pvidConfig(5, 4095, ""), `must be between 1 and 4094`},
		{"port zero", pvidConfig(0, 1, ""), `must be between 1 and 32`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(newPVIDFake()),
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}

func TestAccVLAN8021QPVIDDriftAndGoneState(t *testing.T) {
	f := newPVIDFake()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: pvidConfig(5, 20, "")},
			{
				PreConfig:          func() { f.mutate(func(f *fakeClient) { f.vlans.pvids[5] = 1 }) },
				Config:             pvidConfig(5, 20, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			// the switch stops reporting a PVID for the port: the resource is planned for creation again
			{
				PreConfig:          func() { f.mutate(func(f *fakeClient) { delete(f.vlans.pvids, 5) }) },
				Config:             pvidConfig(5, 20, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{PreConfig: func() { f.mutate(func(f *fakeClient) { f.vlans.pvids[5] = 20 }) }, Config: pvidConfig(5, 20, "")},
		},
	})
}

func TestAccVLAN8021QPVIDDestroyLeavesAnUnrestorablePortAlone(t *testing.T) {
	// VLAN 1 is gone, so there is nothing to restore and the destroy writes nothing
	f := newPVIDFake()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: pvidConfig(5, 20, "")},
			{PreConfig: func() { f.mutate(func(f *fakeClient) { delete(f.vlans.vlans, 1) }) }, Config: providerBlock},
		},
	})
	if err := expectPVID(f, 5, 20); err != nil {
		t.Fatalf("the PVID must be left as it is: %v", err)
	}
}

func TestAccVLAN8021QPVIDDestroyIsGuarded(t *testing.T) {
	f := newPVIDFake()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: pvidConfig(5, 20, "")},
			// port 5 gets a link: restoring its PVID would now cut it
			{
				PreConfig: func() {
					f.mutate(func(f *fakeClient) {
						for i := range f.ports {
							if f.ports[i].ID == 5 {
								f.ports[i].ActualSpeed = "1000Mbps-full"
							}
						}
					})
				},
				Config:      providerBlock,
				ExpectError: regexp.MustCompile(`Refusing to change the PVID`),
			},
			{PreConfig: func() {
				f.mutate(func(f *fakeClient) {
					for i := range f.ports {
						f.ports[i].ActualSpeed = "link-down"
					}
				})
			}, Config: providerBlock},
		},
	})
}

func TestAccVLAN8021QPVIDReportsFailures(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *faultyClient)
		want  string
	}{
		{"state read fails", func(w *faultyClient) { w.failAlways("Dot1Q") }, `Reading the 802.1Q state failed`},
		{"write fails", func(w *faultyClient) { w.failAlways("SetPVIDs") }, `Setting the PVID failed`},
		{"write is accepted but ignored", func(w *faultyClient) { w.ignoreOnly("SetPVIDs") }, `did not change as requested`},
		{"read back fails", func(w *faultyClient) { w.failCalls("Dot1Q", 2, 0) }, `did not change as requested`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newFaulty(newPVIDFake())
			tc.setup(w)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(w),
				Steps:                    []resource.TestStep{{Config: pvidConfig(5, 20, ""), ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}

func TestAccVLAN8021QPVIDDestroyFailures(t *testing.T) {
	t.Run("the state cannot be read at destroy", func(t *testing.T) {
		t.Parallel()
		w := newFaulty(newPVIDFake())
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(w),
			Steps: []resource.TestStep{
				{Config: pvidConfig(5, 20, "")},
				// the refresh before the destroy is the first read; the destroy itself is the second
				{PreConfig: func() { w.heal(); w.failCalls("Dot1Q", 2, 2) }, Config: providerBlock, ExpectError: regexp.MustCompile(`Reading the 802.1Q state failed`)},
				{PreConfig: w.heal, Config: providerBlock},
			},
		})
	})
	t.Run("the state cannot be read while refreshing", func(t *testing.T) {
		t.Parallel()
		w := newFaulty(newPVIDFake())
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(w),
			Steps: []resource.TestStep{
				{Config: pvidConfig(5, 20, "")},
				{PreConfig: func() { w.heal(); w.failAlways("Dot1Q") }, Config: pvidConfig(5, 20, ""), PlanOnly: true, ExpectError: regexp.MustCompile(`Reading the 802.1Q state failed`)},
				{PreConfig: w.heal, Config: providerBlock},
			},
		})
	})
}

func TestAccVLAN8021QPVIDImportRejectsBadIDs(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(newPVIDFake()),
		Steps: []resource.TestStep{
			{Config: pvidConfig(5, 20, "")},
			{ResourceName: "tplinkeasysmart_vlan_8021q_pvid.p", ImportState: true, ImportStateId: "five", ExpectError: regexp.MustCompile(`expected a port number`)},
		},
	})
}

func TestPVIDOfAndFindVLAN(t *testing.T) {
	q := escp.Dot1Q{VLANs: []escp.VLAN8021Q{{ID: 1}, {ID: 20}}, PVIDs: []escp.PortPVID{{Port: 5, PVID: 20}}}
	if got, ok := pvidOf(q, 5); !ok || got != 20 {
		t.Fatalf("got %d %t", got, ok)
	}
	if _, ok := pvidOf(q, 6); ok {
		t.Fatal("a port without a PVID entry has none")
	}
	if v, ok := findVLAN(q.VLANs, 20); !ok || v.ID != 20 {
		t.Fatal("VLAN 20 must be found")
	}
	if _, ok := findVLAN(q.VLANs, 21); ok {
		t.Fatal("VLAN 21 does not exist")
	}
}

// ---- the remaining edges ----

func TestAccVLANModeSwitchesBackToPortBased(t *testing.T) {
	f := newVLANFake("802.1q")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: modeConfig("  mode = \"port-based\"\n  allow_disruptive = true\n"),
			Check: func(*terraformState) error {
				if v := f.vlanState(); !v.portBased || v.enabled {
					return fmt.Errorf("switch is not in port-based mode: %+v", v)
				}
				return nil
			},
		}},
	})
}

func TestAccVLANModeReadFailsWhileRefreshing(t *testing.T) {
	w := newFaulty(newVLANFake("port-based"))
	cfg := modeConfig("  mode = \"port-based\"\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(w),
		Steps: []resource.TestStep{
			{Config: cfg},
			{PreConfig: func() { w.heal(); w.failAlways("VLANMode") }, Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`Reading the VLAN mode failed`)},
		},
	})
}

// Values that are only known after another resource is applied are not validated while planning;
// the client checks them at apply time.
func TestAccVLANConfigurationWithValuesKnownOnlyAfterApply(t *testing.T) {
	f := newVLANFake("port-based")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{Config: providerBlock + `
resource "terraform_data" "mode" {
  input = "802.1q"
}
resource "tplinkeasysmart_vlan_mode" "m" {
  mode             = terraform_data.mode.output
  allow_disruptive = true
}
resource "terraform_data" "port" {
  input = 5
}
resource "tplinkeasysmart_vlan_8021q_group" "v" {
  vlan           = 20
  untagged_ports = [tonumber(terraform_data.port.output)]
  depends_on     = [tplinkeasysmart_vlan_mode.m]
}
`, Check: func(*terraformState) error { return expectVLANPorts(f, 20, 0, escp.MustPortSet(5)) }}},
	})
}

func TestVLANGroupApplyRefusesPortSetsThatAreNotKnown(t *testing.T) {
	known := portsToSet(escp.MustPortSet(5))
	unknown := types.SetUnknown(types.Int64Type)
	for name, m := range map[string]vlan8021QGroupModel{
		"tagged ports":   {VLAN: types.Int64Value(20), TaggedPorts: unknown, UntaggedPorts: known},
		"untagged ports": {VLAN: types.Int64Value(20), TaggedPorts: known, UntaggedPorts: unknown},
	} {
		t.Run(name, func(t *testing.T) {
			f := newVLANFake("802.1q")
			r := &vlan8021QGroupResource{src: &switchRegistry{def: f}}
			var got collected
			if r.apply(context.Background(), &m, got.add) {
				t.Fatal("an unknown port set must not be applied")
			}
			if len(got.summaries) != 1 || got.summaries[0] != "Invalid ports" || f.sets != 0 {
				t.Fatalf("diagnostics %+v, writes %d", got, f.sets)
			}
		})
	}
}
