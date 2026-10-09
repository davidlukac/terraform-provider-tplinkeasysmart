package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestDataSourcesReportASwitchThatCannotBeRead(t *testing.T) {
	cases := []struct {
		dataSource string
		method     string // the client call that fails
		want       string // the error the data source reports
	}{
		{"tplinkeasysmart_system_info", "Info", "Reading switch info failed"},
		{"tplinkeasysmart_system_info", "System", "Reading switch system settings failed"},
		{"tplinkeasysmart_ports", "Ports", "Reading ports failed"},
		{"tplinkeasysmart_port_statistics", "PortStatistics", "Reading port statistics failed"},
		{"tplinkeasysmart_lags", "LAGs", "Reading LAGs failed"},
		{"tplinkeasysmart_port_mirror", "PortMirror", "Reading port mirror failed"},
		{"tplinkeasysmart_vlan_mtu", "MTUVLAN", "Reading MTU VLAN failed"},
		{"tplinkeasysmart_vlan_port_based", "PortBasedVLAN", "Reading port-based VLAN failed"},
		{"tplinkeasysmart_vlan_8021q", "VLAN8021Q", "Reading 802.1Q VLAN failed"},
		{"tplinkeasysmart_qos", "QoS", "Reading QoS failed"},
		{"tplinkeasysmart_bandwidth_control", "Bandwidth", "Reading bandwidth control failed"},
		{"tplinkeasysmart_storm_control", "StormControl", "Reading storm control failed"},
	}
	for _, tc := range cases {
		t.Run(tc.dataSource+" "+tc.method, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(&fakeClient{})
			fc.failAlways(tc.method)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps: []resource.TestStep{{
					Config:      providerBlock + `data "` + tc.dataSource + `" "this" {}`,
					ExpectError: regexp.MustCompile(tc.want),
				}},
			})
		})
	}
}
