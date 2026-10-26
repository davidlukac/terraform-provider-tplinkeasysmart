package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The VLAN resources are only managed while their mode is active, but a mode changed outside
// Terraform must not break the refresh of the whole configuration: a read warns and keeps the
// state, and a destroy forgets the resource, writing nothing.

// setMode8021Q turns the fake's 802.1Q mode off (to port-based) or back on without touching its
// tables, as a mode switch behind Terraform's back would leave the part of the state the test cares about.
func setMode8021Q(f *fakeClient, on bool) {
	f.mutate(func(f *fakeClient) { f.vlans.enabled, f.vlans.portBased = on, !on })
}

func TestAccVLAN8021QGroupReadWarnsWhenTheModeIsOff(t *testing.T) {
	f := newVLANFake("802.1q")
	cfg := groupConfig8021Q(20, "  untagged_ports = [6]\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			// the mode is switched off outside Terraform: the plan still works and shows no change
			{PreConfig: func() { setMode8021Q(f, false) }, Config: cfg, PlanOnly: true},
			{PreConfig: func() { setMode8021Q(f, true) }, Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccVLAN8021QGroupDestroyWhileTheModeIsOffOnlyForgets(t *testing.T) {
	f := newVLANFake("802.1q")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: groupConfig8021Q(20, "  untagged_ports = [6]\n")},
			{PreConfig: func() { setMode8021Q(f, false); f.mutate(func(f *fakeClient) { f.sets = 0 }) }, Config: providerBlock},
		},
	})
	if f.sets != 0 {
		t.Fatalf("a destroy while the mode is off must not write, got %d writes", f.sets)
	}
}

func TestAccVLAN8021QPVIDReadWarnsWhenTheModeIsOff(t *testing.T) {
	f := newPVIDFake()
	cfg := pvidConfig(5, 20, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			{PreConfig: func() { setMode8021Q(f, false) }, Config: cfg, PlanOnly: true},
			{PreConfig: func() { setMode8021Q(f, true) }, Config: cfg, PlanOnly: true},
		},
	})
}

func TestAccVLAN8021QPVIDDestroyWhileTheModeIsOffOnlyForgets(t *testing.T) {
	f := newPVIDFake()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: pvidConfig(5, 20, "")},
			{PreConfig: func() { setMode8021Q(f, false); f.mutate(func(f *fakeClient) { f.sets = 0 }) }, Config: providerBlock},
		},
	})
	if f.sets != 0 {
		t.Fatalf("a destroy while the mode is off must not write, got %d writes", f.sets)
	}
}

func TestAccVLANPortBasedGroupReadAndDestroyWhileTheModeIsOff(t *testing.T) {
	f := &fakeClient{ports: lagPorts(), pvlans: map[int][]int{1: {1, 2, 4, 6, 7}}}
	cfg := vlanGroupConfig(2, "  ports = [6, 7]\n")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			// another mode becomes active behind Terraform's back: the plan works, nothing is written
			{PreConfig: func() { f.mutate(func(f *fakeClient) { f.pvlanOff = true }) }, Config: cfg, PlanOnly: true},
			// and the resource can be removed from the configuration: it is only forgotten
			{PreConfig: func() { f.mutate(func(f *fakeClient) { f.sets = 0 }) }, Config: providerBlock},
		},
	})
	if f.sets != 0 {
		t.Fatalf("a destroy while the mode is off must not write, got %d writes", f.sets)
	}
}
