package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Terraform validates every value before it calls apply, so these checks can
// not be reached through a real run. They remain as the last line of defence
// on paths where the wrong input would be destructive (an empty member list
// would delete a LAG or VLAN group), and are exercised directly.

type collected struct{ summaries, details []string }

func (c *collected) add(summary, detail string) {
	c.summaries = append(c.summaries, summary)
	c.details = append(c.details, detail)
}

func TestLAGApplyRefusesAnEmptyMemberList(t *testing.T) {
	f := pvFakes()
	r := &lagResource{src: &switchRegistry{def: f}}
	var got collected
	m := lagResourceModel{Group: types.Int64Value(2), Ports: types.SetValueMust(types.Int64Type, []attr.Value{}), AllowDisruptive: types.BoolValue(true)}
	if r.apply(context.Background(), &m, got.add) {
		t.Fatal("an empty member list must not be applied: it would delete the group")
	}
	if len(got.summaries) != 1 || got.summaries[0] != "Invalid ports" {
		t.Fatalf("diagnostics: %+v", got)
	}
	if f.sets != 0 {
		t.Fatalf("nothing may be written, got %d writes", f.sets)
	}
}

func TestVLANGroupApplyRefusesAnEmptyMemberList(t *testing.T) {
	f := pvFakes()
	r := &portVLANResource{src: &switchRegistry{def: f}}
	var got collected
	m := portVLANResourceModel{VLAN: types.Int64Value(2), Ports: types.SetValueMust(types.Int64Type, []attr.Value{}), AllowDisruptive: types.BoolValue(true)}
	if r.apply(context.Background(), &m, got.add, true) {
		t.Fatal("an empty member list must not be applied: it would delete the group")
	}
	if len(got.summaries) != 1 || got.summaries[0] != "Invalid ports" {
		t.Fatalf("diagnostics: %+v", got)
	}
	if f.sets != 0 {
		t.Fatalf("nothing may be written, got %d writes", f.sets)
	}
}

func TestQoSApplyRefusesWhatTheSwitchWouldIgnoreOrMisread(t *testing.T) {
	prio := func(k string, v int64) types.Map {
		return types.MapValueMust(types.Int64Type, map[string]attr.Value{k: types.Int64Value(v)})
	}
	cases := []struct {
		name    string
		mode    string
		prios   types.Map
		summary string
	}{
		{"unknown mode name", "turbo", prio("7", 2), "Invalid QoS mode"},
		{"priority key that is not a port number", "port-based", prio("seven", 2), "Invalid port key"},
		{"priorities outside port-based mode", "802.1p", prio("7", 2), "Priorities need port-based mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := pvFakes()
			r := &qosResource{src: &switchRegistry{def: f}}
			var got collected
			m := qosResourceModel{Mode: types.StringValue(tc.mode), Priorities: tc.prios}
			if r.apply(context.Background(), &m, got.add) {
				t.Fatal("must be refused")
			}
			if len(got.summaries) != 1 || got.summaries[0] != tc.summary || strings.TrimSpace(got.details[0]) == "" {
				t.Fatalf("diagnostics: %+v", got)
			}
			if f.sets != 0 || f.qos.Mode != 2 {
				t.Fatalf("nothing may be written: sets=%d mode=%d", f.sets, f.qos.Mode)
			}
		})
	}
}

func TestManagementIPApplyStopsOnAnAddressThatIsNotIPv4(t *testing.T) {
	f := pvFakes()
	r := &managementIPResource{src: &switchRegistry{def: f}}
	var got collected
	m := managementIPModel{IPAddress: types.StringValue("not-an-address"), Netmask: types.StringValue("255.255.255.0"), Gateway: types.StringValue("192.168.77.1"),
		DHCP: types.BoolValue(false), AllowDisruptive: types.BoolValue(true)}
	if r.apply(context.Background(), &m, got.add) {
		t.Fatal("an address that is not IPv4 must not be applied")
	}
	if len(got.summaries) != 1 || got.summaries[0] != "Not an IPv4 address" || !strings.Contains(got.details[0], "not-an-address") {
		t.Fatalf("want one diagnostic naming the value, got %+v", got)
	}
	if f.sets != 0 {
		t.Fatalf("nothing may be written, got %d writes", f.sets)
	}
}
