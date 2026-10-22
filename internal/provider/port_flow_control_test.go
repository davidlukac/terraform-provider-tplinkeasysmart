package provider

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestPortFlowControlProblem(t *testing.T) {
	cases := []struct {
		name  string
		speed types.String
		flow  types.Bool
		bad   bool
	}{
		{"nothing set", types.StringNull(), types.BoolNull(), false},
		{"flow control off at a fixed speed", types.StringValue("100Mbps-full"), types.BoolValue(false), false},
		{"flow control omitted at a fixed speed", types.StringValue("100Mbps-full"), types.BoolNull(), false},
		{"flow control on, speed omitted (defaults to auto)", types.StringNull(), types.BoolValue(true), false},
		{"flow control on with auto", types.StringValue("auto"), types.BoolValue(true), false},
		{"flow control on with 1000 Mbps full", types.StringValue("1000Mbps-full"), types.BoolValue(true), false},
		{"flow control on with 100 Mbps full", types.StringValue("100Mbps-full"), types.BoolValue(true), true},
		{"flow control on with 100 Mbps half", types.StringValue("100Mbps-half"), types.BoolValue(true), true},
		{"flow control on with 10 Mbps full", types.StringValue("10Mbps-full"), types.BoolValue(true), true},
		{"flow control on with 10 Mbps half", types.StringValue("10Mbps-half"), types.BoolValue(true), true},
		{"speed not known yet", types.StringUnknown(), types.BoolValue(true), false},
		{"flow control not known yet", types.StringValue("100Mbps-full"), types.BoolUnknown(), false},
		{"a speed the speed validator rejects is not judged twice", types.StringValue("warp"), types.BoolValue(true), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := portFlowControlProblem(tc.speed, tc.flow)
			if (got != "") != tc.bad {
				t.Fatalf("problem = %q, want a problem: %v", got, tc.bad)
			}
			if tc.bad {
				for _, part := range []string{`"auto"`, `"1000Mbps-full"`, tc.speed.ValueString()} {
					if !strings.Contains(got, part) {
						t.Errorf("the message %q does not mention %s", got, part)
					}
				}
			}
		})
	}
}

// The combinations the switch keeps still apply and read back, and a change between them works.
func TestAccPortFlowControlWithTheSpeedsThatHoldIt(t *testing.T) {
	f := &fakeClient{ports: testPorts()}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: portConfig(7, "  speed = \"1000Mbps-full\"\n  flow_control = true\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "speed", "1000Mbps-full"),
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "flow_control", "true"))},
			{Config: portConfig(7, "  flow_control = true\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "speed", "auto"),
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "flow_control", "true"))},
			{Config: portConfig(7, "  speed = \"100Mbps-full\"\n"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "speed", "100Mbps-full"),
				resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "flow_control", "false"))},
		},
	})
}
