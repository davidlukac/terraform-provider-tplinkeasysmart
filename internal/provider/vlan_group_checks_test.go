package provider

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

// A port-based VLAN group is checked against what the switch has before anything is written: the id
// (1 to the number of groups it reports, as the web UI takes them), that the group does not already
// exist when it is created, and the ports (the client refuses ports the switch lacks even when the
// guard is overridden).

func TestAccVLANGroupRefusesAnIdTheSwitchDoesNotHave(t *testing.T) {
	f := &fakeClient{ports: lagPorts(), pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)}} // the fake reports 8 groups
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: vlanGroupConfig(9, "  ports = [6, 7]\n"), ExpectError: regexp.MustCompile(`(?s)Invalid VLAN group.*supports\s+ids\s+1-8`)},
			{Config: vlanGroupConfig(255, "  ports = [6, 7]\n  allow_disruptive = true\n"), ExpectError: regexp.MustCompile(`(?s)Invalid VLAN group.*supports\s+ids\s+1-8`)},
		},
	})
	if f.sets != 0 {
		t.Fatalf("an id the switch does not have must not reach it, got %d writes", f.sets)
	}
}

func TestAccVLANGroupRefusesToCreateOneThatExists(t *testing.T) {
	f := &fakeClient{ports: lagPorts(), pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4), 2: escp.MustPortSet(6)}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			// group 2 exists with port 6: a second resource for it would overwrite it, even with the guard overridden
			{Config: vlanGroupConfig(2, "  ports = [7]\n  allow_disruptive = true\n"), ExpectError: regexp.MustCompile(`(?s)VLAN group already exists.*group 2 is already on the switch \(ports 6\).*terraform import <address> 2`)},
		},
	})
	if f.sets != 0 {
		t.Fatalf("an existing group must not be overwritten by a create, got %d writes", f.sets)
	}
	if got := f.pvlans[2]; got != escp.MustPortSet(6) {
		t.Fatalf("group 2 = %v, want it untouched", got)
	}
}

// Two resources for the same id in one configuration: the first creates the group, the second finds
// it and is refused instead of taking the ports of the first.
func TestAccVLANGroupTwoResourcesForOneIdAreRefused(t *testing.T) {
	f := &fakeClient{ports: lagPorts(), pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)}}
	cfg := providerBlock + `
resource "tplinkeasysmart_vlan_port_based_group" "a" {
  vlan  = 2
  ports = [6]
}
resource "tplinkeasysmart_vlan_port_based_group" "b" {
  vlan       = 2
  ports      = [7]
  depends_on = [tplinkeasysmart_vlan_port_based_group.a]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`(?s)VLAN group already exists.*group 2 is already on the switch \(ports 6\)`)}},
	})
	// the first resource's creation and, when the test ends, its destruction; the second wrote nothing
	if f.sets != 2 {
		t.Fatalf("%d writes, want 2 (create and destroy of the first resource only)", f.sets)
	}
}

// The guard still covers the old members of a group that is updated.
func TestAccVLANGroupUpdateGuardCoversTheOldMembers(t *testing.T) {
	f := &fakeClient{ports: lagPorts(), pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			// port 4 has a link up: the group is created with the override
			{Config: vlanGroupConfig(2, "  ports = [4, 6]\n  allow_disruptive = true\n")},
			// dropping port 4 moves it back to VLAN 1, which the guard refuses without the override
			{Config: vlanGroupConfig(2, "  ports = [6]\n"), ExpectError: regexp.MustCompile(`port 4 has a link up`)},
		},
	})
}

// With the real client against the fake switch, the client's own checks hold when the guard is
// overridden: nothing reaches the wire for an id or a port the switch lacks.
func TestAccRealClientRefusesAGroupIdOrAPortTheSwitchLacks(t *testing.T) {
	sw := escptest.Start(t)
	sw.EnableModel(map[int]escptest.LinkSpeed{1: escptest.Link1000Full})
	group := func(id int, ports string) string {
		return realClientProvider("") + `
resource "tplinkeasysmart_vlan_port_based_group" "v" {
  vlan             = ` + strconv.Itoa(id) + `
  ports            = ` + ports + `
  allow_disruptive = true
}
`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: realClientFactories(t, sw),
		Steps: []resource.TestStep{
			{Config: group(9, "[2]"), ExpectError: regexp.MustCompile(`(?s)Invalid VLAN group.*supports\s+ids\s+1-8`)},
			{Config: group(2, "[2, 9]"), ExpectError: regexp.MustCompile(`(?s)port\s+9\s+does\s+not\s+exist`)},
		},
	})
	if sets := sw.AllSets(); len(sets) != 0 {
		t.Fatalf("the switch received %d write(s): %v", len(sets), sets)
	}
}
