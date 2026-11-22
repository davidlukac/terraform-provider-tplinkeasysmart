package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go/model"
)

// The resources below are identified by the key the user writes (a port, a LAG group, a VLAN), not by an
// `id` attribute. Their import id is that key, prefixed with the switch name for a named switch
// ("sw02:7"). These tests hold that identity as it is: the same resource and the same import id give the
// same state for the provider's own switch and for a named one, and the data source that reports the
// switch's MAC address keeps the MAC as its id on both (it has never carried the switch prefix).
// DC-P04 (stable identity), DC-C01 (a documented promise has a test).

// keyedResource is one resource that is identified by a key attribute.
type keyedResource struct {
	name   string             // the resource type without the provider prefix
	key    string             // the attribute that holds the key, also the import verify identifier
	keyVal string             // its value in the configuration
	body   string             // the arguments after the key, one per line
	fake   func() *fakeClient // the switch behind it
}

func keyedResources() []keyedResource {
	return []keyedResource{
		{"port", "port", "7", "  flow_control = true\n", func() *fakeClient { return &fakeClient{ports: testPorts()} }},
		{"bandwidth_limit", "port", "7", "  ingress_kbps = 1024\n", func() *fakeClient {
			return &fakeClient{ports: testPorts(), bw: map[model.PortID][2]int{}}
		}},
		{"storm_limit", "port", "7", "  broadcast = true\n  rate_kbps = 1024\n", func() *fakeClient {
			return &fakeClient{ports: testPorts(), storm: map[model.PortID]model.PortStorm{}}
		}},
		{"lag", "group", "2", "  ports = [6, 7]\n", func() *fakeClient {
			return &fakeClient{ports: lagPorts(), lags: map[model.LAGID]model.PortSet{1: model.MustPortSet(1, 2)}}
		}},
		{"vlan_port_based_group", "vlan", "2", "  ports = [6, 7]\n", func() *fakeClient {
			return &fakeClient{ports: lagPorts(), pvlans: map[model.PortVLANID]model.PortSet{1: model.MustPortSet(1, 2, 4, 6, 7)}}
		}},
		{"vlan_8021q_group", "vlan", "20", "  untagged_ports = [6]\n", func() *fakeClient { return newVLANFake("802.1q") }},
		{"vlan_8021q_pvid", "port", "5", "  pvid = 20\n", newPVIDFake},
	}
}

// config is the resource written for the provider's own switch (named false) or for sw02.
func (k keyedResource) config(named bool) string {
	switchArg, head := "", providerBlock
	if named {
		switchArg, head = "  switch = \"sw02\"\n", twoSwitches
	}
	return head + fmt.Sprintf("resource \"tplinkeasysmart_%s\" \"r\" {\n%s  %s = %s\n%s}\n", k.name, switchArg, k.key, k.keyVal, k.body)
}

func TestKeyedResourcesKeepTheirIdentityAndImportIdOnEverySwitch(t *testing.T) {
	for _, k := range keyedResources() {
		for _, named := range []bool{false, true} {
			name := k.name + "/the provider's own switch"
			importID, wantSwitch := k.keyVal, ""
			if named {
				name, importID, wantSwitch = k.name+"/a named switch", "sw02:"+k.keyVal, "sw02"
			}
			t.Run(name, func(t *testing.T) {
				clearProviderEnv(t)
				addr := "tplinkeasysmart_" + k.name + ".r"
				this, other := k.fake(), k.fake()
				factory := factories(this)
				if named {
					factory = multiFactories(map[string]SwitchClient{"10.0.0.1": other, "10.0.0.2": this}, nil)
				}
				switchCheck := resource.TestCheckNoResourceAttr(addr, "switch")
				if named {
					switchCheck = resource.TestCheckResourceAttr(addr, "switch", wantSwitch)
				}
				resource.UnitTest(t, resource.TestCase{
					ProtoV6ProviderFactories: factory,
					Steps: []resource.TestStep{
						{Config: k.config(named), Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(addr, k.key, k.keyVal),
							resource.TestCheckNoResourceAttr(addr, "id"),
							switchCheck,
						)},
						{ResourceName: addr, ImportState: true, ImportStateId: importID, ImportStateVerify: true,
							ImportStateVerifyIdentifierAttribute: k.key, ImportStateVerifyIgnore: []string{"allow_disruptive"}},
						{Config: k.config(named), PlanOnly: true},
					},
				})
				if named && other.sets != 0 {
					t.Errorf("the other switch received %d write(s)", other.sets)
				}
			})
		}
	}
}

func TestSystemInfoKeepsTheMACAddressAsItsIdOnEverySwitch(t *testing.T) {
	cases := []struct {
		name  string
		named bool
	}{{"the provider's own switch", false}, {"a named switch", true}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnv(t)
			const addr = "data.tplinkeasysmart_system_info.s"
			factory, cfg := factories(&fakeClient{}), providerBlock+"data \"tplinkeasysmart_system_info\" \"s\" {}\n"
			switchCheck := resource.TestCheckNoResourceAttr(addr, "switch")
			if tc.named {
				factory = multiFactories(map[string]SwitchClient{"10.0.0.1": &fakeClient{}, "10.0.0.2": &fakeClient{}}, nil)
				cfg = twoSwitches + "data \"tplinkeasysmart_system_info\" \"s\" {\n  switch = \"sw02\"\n}\n"
				switchCheck = resource.TestCheckResourceAttr(addr, "switch", "sw02")
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factory,
				Steps: []resource.TestStep{{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "id", fakeMAC.String()),
					resource.TestCheckResourceAttr(addr, "mac_address", fakeMAC.String()),
					switchCheck,
				)}},
			})
		})
	}
}

// The switch prefix of an import id names the switch the object is read from. A prefix that no switch has
// is refused with the switch error and never read from another switch, and an id without a prefix does not
// mean any named switch: beside a `switches` map the provider asks which one is meant.
func TestKeyedResourcesRefuseAnImportWithAnUnknownOrMissingSwitchPrefix(t *testing.T) {
	cases := []struct {
		name     string
		importID func(k keyedResource) string
		wantErr  string
	}{
		{"an unknown prefix", func(k keyedResource) string { return "sw09:" + k.keyVal }, `unknown switch "sw09"`},
		{"no prefix", func(k keyedResource) string { return k.keyVal }, `(?s)must say\s+which switch it is for`},
	}
	for _, k := range keyedResources() {
		for _, tc := range cases {
			t.Run(k.name+"/"+tc.name, func(t *testing.T) {
				clearProviderEnv(t)
				addr := "tplinkeasysmart_" + k.name + ".r"
				this, other := k.fake(), k.fake()
				resource.UnitTest(t, resource.TestCase{
					ProtoV6ProviderFactories: multiFactories(map[string]SwitchClient{"10.0.0.1": other, "10.0.0.2": this}, nil),
					Steps: []resource.TestStep{
						{Config: k.config(true)},
						{ResourceName: addr, ImportState: true, ImportStateId: tc.importID(k), ExpectError: regexp.MustCompile(tc.wantErr)},
					},
				})
			})
		}
	}
}
