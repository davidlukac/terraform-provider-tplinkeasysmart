package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go"
)

// pvFakes returns a fresh fake switch for every case: ports 1/2 in LAG1 and
// linked, 4 linked, 6/7 free; LAG1 present; QoS in DSCP mode; mirroring off.
func pvFakes() *fakeClient {
	f := newFakeQoS()
	f.ports = lagPorts()
	f.lags = map[escp.LAGID]escp.PortSet{1: escp.MustPortSet(1, 2)}
	f.pvlans = map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)}
	f.mirror = &escp.Mirror{}
	f.storm = map[escp.PortID]escp.PortStorm{}
	f.bw = map[escp.PortID][2]int{}
	return f
}

// Every rule the schemas can check must fire while Terraform plans, before the
// provider is configured to write anything.
func TestInvalidConfigurationIsRejectedWhilePlanning(t *testing.T) {
	cases := []struct{ name, cfg, want string }{
		{"port number zero", portConfig(0, ""), `(?s)port.*between 1 and 32`},
		{"port number above the maximum", portConfig(33, ""), `(?s)port.*between 1 and 32`},
		{"unknown speed", portConfig(7, "  speed = \"warp\"\n"), `must be one of`},
		{"flow control at 100 Mbps full", portConfig(7, "  speed = \"100Mbps-full\"\n  flow_control = true\n"), `(?s)flow_control\s+=\s+true\s+needs\s+speed`},
		{"flow control at 100 Mbps half", portConfig(7, "  speed = \"100Mbps-half\"\n  flow_control = true\n"), `(?s)flow_control\s+=\s+true\s+needs\s+speed`},
		{"flow control at 10 Mbps full", portConfig(7, "  speed = \"10Mbps-full\"\n  flow_control = true\n"), `(?s)flow_control\s+=\s+true\s+needs\s+speed`},
		{"flow control at 10 Mbps half", portConfig(7, "  speed = \"10Mbps-half\"\n  flow_control = true\n"), `(?s)flow_control\s+=\s+true\s+needs\s+speed`},
		{"bandwidth rate not a multiple of 64", bwConfig(7, "  ingress_kbps = 100\n"), `multiple of 64`},
		{"bandwidth rate above the maximum", bwConfig(7, "  egress_kbps = 1000064\n"), `multiple of 64`},
		{"bandwidth port out of range", bwConfig(40, ""), `between 1 and 32`},
		{"storm type without a rate", stormConfig(7, "  broadcast = true\n"), `rate_kbps must be`},
		{"storm rate without a type", stormConfig(7, "  rate_kbps = 64\n"), `no traffic type`},
		{"storm rate not a multiple of 64", stormConfig(7, "  broadcast = true\n  rate_kbps = 100\n"), `multiple of 64`},
		{"mirror enabled without destination", mirrorConfig("  enabled = true\n  ingress_ports = [4]\n"), `mirror_port must be set`},
		{"mirror enabled without sources", mirrorConfig("  enabled = true\n  mirror_port = 7\n"), `at least one ingress or egress`},
		{"mirror cannot mirror itself", mirrorConfig("  enabled = true\n  mirror_port = 7\n  ingress_ports = [7]\n"), `cannot mirror itself`},
		{"disabled mirror with ports", mirrorConfig("  enabled = false\n  mirror_port = 7\n"), `(?s)must\s+be\s+empty`},
		{"mirror source out of range", mirrorConfig("  enabled = true\n  mirror_port = 7\n  ingress_ports = [40]\n"), `between 1 and 32`},
		{"mirror destination out of range", mirrorConfig("  enabled = true\n  mirror_port = 40\n  ingress_ports = [4]\n"), `between 0 and 32`},
		{"LAG group zero", lagConfig(0, "  ports = [6, 7]\n"), `between 1 and 2`},
		{"LAG group three", lagConfig(3, "  ports = [6, 7]\n"), `between 1 and 2`},
		{"LAG without members", lagConfig(2, "  ports = []\n"), `at least 2`},
		{"LAG with one member", lagConfig(2, "  ports = [6]\n"), `at least 2`},
		{"LAG with five members", lagConfig(1, "  ports = [1, 2, 3, 4, 5]\n"), `at most 4`},
		{"LAG member out of range", lagConfig(2, "  ports = [6, 40]\n"), `between 1 and 32`},
		{"LAG 1 reaching into the ports of LAG 2", lagConfig(1, "  ports = [4, 5]\n"), `(?s)port 5 cannot be in LAG 1.*ports 1-4`},
		{"LAG 2 with low ports", lagConfig(2, "  ports = [2, 3]\n"), `(?s)port 2 cannot be in LAG 2.*ports 5-8`},
		{"VLAN id zero", vlanGroupConfig(0, "  ports = [6, 7]\n"), `between 1 and 255`},
		{"VLAN group without members", vlanGroupConfig(2, "  ports = []\n"), `at least 1`},
		{"VLAN member out of range", vlanGroupConfig(2, "  ports = [0]\n"), `between 1 and 32`},
		{"QoS unknown mode", qosCfg("  mode = \"turbo\"\n"), `must be one of`},
		{"QoS priority above 4", qosCfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 5 }\n"), `between 1 and 4`},
		{"QoS priority key not a port", qosCfg("  mode = \"port-based\"\n  port_priorities = { \"seven\" = 2 }\n"), `not a port number`},
		{"QoS priority key out of range", qosCfg("  mode = \"port-based\"\n  port_priorities = { \"40\" = 2 }\n"), `not a port number`},
		{"QoS priorities outside port-based mode", qosCfg("  mode = \"802.1p\"\n  port_priorities = { \"7\" = 2 }\n"), `only take effect when mode is port-based`},
		{"hostname with a dot", systemConfig("sw01.local"), `(?s)letters,\s+digits\s+and\s+hyphens`},
		{"hostname starting with a hyphen", systemConfig("-sw01"), `letters, digits and hyphens`},
		{"hostname too long", systemConfig(strings.Repeat("a", 33)), `(?s)hostname.*(32|length)`},
		{"empty hostname", systemConfig(""), `(?s)Invalid\s+device\s+description`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pvFakes()
			f.extra().active, f.extra().hostname = true, "TL-SG108E-01"
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: tc.cfg, PlanOnly: true, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if f.sets != 0 || f.extra().setCalls != 0 {
				t.Fatalf("a configuration rejected while planning must not write: sets=%d hostname writes=%d", f.sets, f.extra().setCalls)
			}
		})
	}
}

// Values that are only known after another resource is applied cannot be
// checked while planning. Terraform validates them again as soon as they are
// known, before it applies the resource, so they are still refused before
// anything is written.
func TestValuesUnknownWhilePlanningAreValidatedBeforeAnythingIsWritten(t *testing.T) {
	cases := []struct{ name, cfg, want string }{
		{"LAG members decided later and empty", providerBlock + "resource \"terraform_data\" \"p\" {\n  input = []\n}\n" +
			"resource \"tplinkeasysmart_lag\" \"l\" {\n  group = 2\n  ports = terraform_data.p.output\n}\n", `at least 2`},
		{"VLAN members decided later and empty", providerBlock + "resource \"terraform_data\" \"p\" {\n  input = []\n}\n" +
			"resource \"tplinkeasysmart_vlan_port_based_group\" \"v\" {\n  vlan = 2\n  ports = terraform_data.p.output\n}\n", `at least 1`},
		{"port speed decided later and fixed, with flow control", providerBlock + "resource \"terraform_data\" \"s\" {\n  input = \"100Mbps-full\"\n}\n" +
			"resource \"tplinkeasysmart_port\" \"p\" {\n  port = 7\n  speed = terraform_data.s.output\n  flow_control = true\n}\n", `(?s)flow_control\s+=\s+true\s+needs\s+speed`},
		{"flow control decided later and on, with a fixed speed", providerBlock + "resource \"terraform_data\" \"f\" {\n  input = true\n}\n" +
			"resource \"tplinkeasysmart_port\" \"p\" {\n  port = 7\n  speed = \"10Mbps-full\"\n  flow_control = terraform_data.f.output\n}\n", `(?s)flow_control\s+=\s+true\s+needs\s+speed`},
		{"QoS mode decided later and invalid", providerBlock + "resource \"terraform_data\" \"m\" {\n  input = \"turbo\"\n}\n" +
			"resource \"tplinkeasysmart_qos\" \"q\" {\n  mode = terraform_data.m.output\n}\n", `must be one of`},
		{"QoS priorities decided later outside port-based mode", providerBlock + "resource \"terraform_data\" \"m\" {\n  input = \"802.1p\"\n}\n" +
			"resource \"tplinkeasysmart_qos\" \"q\" {\n  mode = terraform_data.m.output\n  port_priorities = { \"7\" = 2 }\n}\n", `only take effect when mode is port-based`},
		{"QoS priority key decided later and invalid", providerBlock + "resource \"terraform_data\" \"k\" {\n  input = { seven = 2 }\n}\n" +
			"resource \"tplinkeasysmart_qos\" \"q\" {\n  mode = \"port-based\"\n  port_priorities = terraform_data.k.output\n}\n", `Invalid port key`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pvFakes()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if f.sets != 0 {
				t.Fatalf("nothing may be written, got %d writes", f.sets)
			}
		})
	}
}

// ports a mirror takes from another resource are unknown while planning: the
// plan-time rule is skipped and the apply goes through normally.
func TestMirrorSourcesKnownOnlyAtApplyAreAccepted(t *testing.T) {
	f := pvFakes()
	cfg := providerBlock + "resource \"terraform_data\" \"src\" {\n  input = [4]\n}\n" +
		"resource \"tplinkeasysmart_port_mirror\" \"m\" {\n  enabled = true\n  mirror_port = 7\n  ingress_ports = terraform_data.src.output\n}\n"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps:                    []resource.TestStep{{Config: cfg, Check: resource.TestCheckResourceAttr("tplinkeasysmart_port_mirror.m", "ingress_ports.#", "1")}},
	})
}

func TestStormProblem(t *testing.T) {
	yes, no, unk := types.BoolValue(true), types.BoolValue(false), types.BoolUnknown()
	rate := types.Int64Value
	cases := []struct {
		name            string
		uu, mc, bc      types.Bool
		rate            types.Int64
		wantProblemPart string
	}{
		{"no type and no rate", no, no, no, rate(0), ""},
		{"a type with a valid rate", no, yes, no, rate(64), ""},
		{"every type with a valid rate", yes, yes, yes, rate(1000000 - 1000000%64), ""},
		{"a type without a rate", no, no, yes, rate(0), "rate_kbps must be"},
		{"a type with a rate below one step", yes, no, no, rate(63), "rate_kbps must be"},
		{"a rate without a type", no, no, no, rate(64), "no traffic type"},
		{"unknown type is skipped", unk, no, no, rate(0), ""},
		{"unknown rate is skipped", no, no, yes, types.Int64Unknown(), ""},
	}
	for _, tc := range cases {
		got := stormProblem(tc.uu, tc.mc, tc.bc, tc.rate)
		if (tc.wantProblemPart == "") != (got == "") || !strings.Contains(got, tc.wantProblemPart) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, got, tc.wantProblemPart)
		}
	}
}

func TestMirrorProblem(t *testing.T) {
	yes, no := types.BoolValue(true), types.BoolValue(false)
	cases := []struct {
		name            string
		enabled         types.Bool
		dest            types.Int64
		ingress, egress escp.PortSet
		wantProblemPart string
	}{
		{"disabled and empty", no, types.Int64Value(0), 0, 0, ""},
		{"enabled and complete", yes, types.Int64Value(7), escp.MustPortSet(4), 0, ""},
		{"enabled with egress only", yes, types.Int64Value(7), 0, escp.MustPortSet(1, 2), ""},
		{"disabled with a destination", no, types.Int64Value(7), 0, 0, "must be empty"},
		{"disabled with sources", no, types.Int64Value(0), escp.MustPortSet(4), 0, "must be empty"},
		{"enabled without destination", yes, types.Int64Value(0), escp.MustPortSet(4), 0, "mirror_port must be set"},
		{"enabled without sources", yes, types.Int64Value(7), 0, 0, "at least one"},
		{"destination mirrors itself via ingress", yes, types.Int64Value(7), escp.MustPortSet(7), 0, "cannot mirror itself"},
		{"destination mirrors itself via egress", yes, types.Int64Value(7), escp.MustPortSet(4), escp.MustPortSet(7), "cannot mirror itself"},
		{"unknown enabled is skipped", types.BoolUnknown(), types.Int64Value(0), 0, 0, ""},
		{"unknown destination is skipped", yes, types.Int64Unknown(), 0, 0, ""},
	}
	for _, tc := range cases {
		got := mirrorProblem(tc.enabled, tc.dest, tc.ingress, tc.egress)
		if (tc.wantProblemPart == "") != (got == "") || !strings.Contains(got, tc.wantProblemPart) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, got, tc.wantProblemPart)
		}
	}
}

func TestQoSProblem(t *testing.T) {
	prios := types.MapValueMust(types.Int64Type, map[string]attr.Value{"7": types.Int64Value(2)})
	none := types.MapValueMust(types.Int64Type, map[string]attr.Value{})
	cases := []struct {
		name       string
		mode       types.String
		priorities types.Map
		want       bool
	}{
		{"port-based with priorities", types.StringValue("port-based"), prios, false},
		{"dscp without priorities", types.StringValue("dscp-802.1p"), none, false},
		{"dscp with priorities", types.StringValue("dscp-802.1p"), prios, true},
		{"802.1p with priorities", types.StringValue("802.1p"), prios, true},
		{"unknown mode is skipped", types.StringUnknown(), prios, false},
		{"unknown priorities are skipped", types.StringValue("802.1p"), types.MapUnknown(types.Int64Type), false},
	}
	for _, tc := range cases {
		if got := qosProblem(tc.mode, tc.priorities) != ""; got != tc.want {
			t.Errorf("%s: problem=%v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRateValidator(t *testing.T) {
	v := rateKbps{}
	if v.Description(context.Background()) == "" || v.MarkdownDescription(context.Background()) != v.Description(context.Background()) {
		t.Fatal("the validator must describe itself, in plain and markdown form")
	}
	for _, tc := range []struct {
		in      types.Int64
		invalid bool
	}{
		{types.Int64Value(0), false}, {types.Int64Value(64), false}, {types.Int64Value(1000000 - 1000000%64), false},
		{types.Int64Value(100), true}, {types.Int64Value(-64), true}, {types.Int64Value(1000064), true},
		{types.Int64Null(), false}, {types.Int64Unknown(), false},
	} {
		var resp validator.Int64Response
		v.ValidateInt64(context.Background(), validator.Int64Request{ConfigValue: tc.in}, &resp)
		if resp.Diagnostics.HasError() != tc.invalid {
			t.Errorf("%v: invalid=%v, want %v", tc.in, resp.Diagnostics.HasError(), tc.invalid)
		}
	}
}

func TestPortKeyValidator(t *testing.T) {
	v := portKey{}
	if v.Description(context.Background()) == "" || v.MarkdownDescription(context.Background()) != v.Description(context.Background()) {
		t.Fatal("the validator must describe itself, in plain and markdown form")
	}
	for _, tc := range []struct {
		in      types.String
		invalid bool
	}{
		{types.StringValue("1"), false}, {types.StringValue("32"), false}, {types.StringValue("0"), true}, {types.StringValue("33"), true},
		{types.StringValue("seven"), true}, {types.StringValue(""), true}, {types.StringNull(), false}, {types.StringUnknown(), false},
	} {
		var resp validator.StringResponse
		v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: tc.in}, &resp)
		if resp.Diagnostics.HasError() != tc.invalid {
			t.Errorf("%v: invalid=%v, want %v", tc.in, resp.Diagnostics.HasError(), tc.invalid)
		}
	}
}

func TestQoSModeNamesAreInModeOrder(t *testing.T) {
	got := qosModeNames()
	want := []string{"port-based", "802.1p", "dscp-802.1p"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v: the order must follow the switch's mode numbers", got, want)
	}
}

func TestDefaultsForOmittedAttributes(t *testing.T) {
	if defaultFalse(types.BoolNull()).ValueBool() || !defaultFalse(types.BoolValue(true)).ValueBool() || !defaultFalse(types.BoolUnknown()).IsUnknown() {
		t.Fatal("defaultFalse: null means false, values and unknown pass through")
	}
	if defaultZero(types.Int64Null()).ValueInt64() != 0 || defaultZero(types.Int64Value(7)).ValueInt64() != 7 || !defaultZero(types.Int64Unknown()).IsUnknown() {
		t.Fatal("defaultZero: null means 0, values and unknown pass through")
	}
}

func TestPortsOfDistinguishesOmittedFromUnknown(t *testing.T) {
	ctx := context.Background()
	if p, ok := portsOf(ctx, types.SetUnknown(types.Int64Type)); ok || p != 0 {
		t.Fatal("an unknown set is not usable yet")
	}
	if p, ok := portsOf(ctx, types.SetNull(types.Int64Type)); !ok || !p.IsEmpty() {
		t.Fatal("an omitted set means no ports")
	}
	set := types.SetValueMust(types.Int64Type, []attr.Value{types.Int64Value(6), types.Int64Value(4)})
	if p, ok := portsOf(ctx, set); !ok || p != escp.MustPortSet(4, 6) {
		t.Fatalf("a known set yields its ports, got %v %v", p, ok)
	}
}

var _ = escp.MaxPort
