package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/davidlukac/escp-go/model"
)

// destroyPlan runs ModifyPlan of r the way Terraform does for a destroy: the prior state is the
// object, the plan is null.
func destroyPlan[T any](t *testing.T, r interface {
	resource.Resource
	resource.ResourceWithModifyPlan
}, state T) diag.Diagnostics {
	t.Helper()
	ctx := context.Background()
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	objType := sch.Schema.Type().TerraformType(ctx)
	st := tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)}
	if diags := st.Set(ctx, &state); diags.HasError() {
		t.Fatal(diags)
	}
	req := resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)}, State: st}
	var resp resource.ModifyPlanResponse
	r.ModifyPlan(ctx, req, &resp)
	return resp.Diagnostics
}

func warningsOf(d diag.Diagnostics) []string {
	var out []string
	for _, w := range d.Warnings() {
		out = append(out, w.Summary()+": "+w.Detail())
	}
	return out
}

func lagState(allow bool, ports ...model.PortID) lagResourceModel {
	return lagResourceModel{Switch: types.StringNull(), Group: types.Int64Value(1), Ports: portsToSet(model.MustPortSet(ports...)), AllowDisruptive: types.BoolValue(allow)}
}

// The allow_disruptive kept in state switches the guard off for a destroy the configuration no longer
// asks for: the destroy plan says so when a port involved has a link or is in a LAG.
func TestADestroyPlanWarnsAboutAStandingOptInOnAPortThatIsInUse(t *testing.T) {
	ports := append(lagPorts(), model.Port{ID: 8, Enabled: true, LAG: 2, ConfiguredSpeed: "auto", ActualSpeed: "link-down"})
	ports[5].LAG = 1 // port 6: an idle member of LAG 1
	reg := &switchRegistry{def: &fakeClient{ports: ports}}
	for _, tt := range []struct {
		name    string
		state   lagResourceModel
		src     switchSource
		want    string // in the single warning; empty: none
		unknown bool
	}{
		{"a port with a link", lagState(true, 3, 4), reg, "port 4 has a link up", false},
		{"a port of another LAG", lagState(true, 3, 8), reg, "port 8 is a member of LAG2", false},
		{"the LAG's own idle members are not a reason", lagState(true, 3, 6), reg, "", false},
		{"the flag is off: the destroy is guarded", lagState(false, 3, 4), reg, "", false},
		{"only free ports", lagState(true, 3, 5), reg, "", false},
		{"a port the switch does not have", lagState(true, 3, 9), reg, "", false},
		{"no ports in the state", lagState(true), reg, "", false},
		{"the provider is not configured", lagState(true, 4), nil, "", false},
		{"a switch that cannot be resolved", lagResourceModel{Switch: types.StringValue("nope"), Group: types.Int64Value(1), Ports: portsToSet(model.MustPortSet(4)), AllowDisruptive: types.BoolValue(true)}, reg, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := warningsOf(destroyPlan(t, &lagResource{src: tt.src}, tt.state))
			switch {
			case tt.want == "" && len(got) != 0:
				t.Fatalf("no warning expected, got %v", got)
			case tt.want != "" && (len(got) != 1 || !strings.Contains(got[0], tt.want)):
				t.Fatalf("want one warning with %q, got %v", tt.want, got)
			}
		})
	}
}

func TestADestroyPlanOfASwitchThatCannotBeReadHasNoWarning(t *testing.T) {
	fc := newFaulty(&fakeClient{ports: lagPorts()})
	fc.failAlways("Ports")
	if got := warningsOf(destroyPlan(t, &lagResource{src: &switchRegistry{def: fc}}, lagState(true, 4))); len(got) != 0 {
		t.Fatalf("a switch that cannot be read at plan time is the apply's business: %v", got)
	}
}

// A plan that is not a destroy, and a state that is gone, are not this warning's business.
func TestOnlyADestroyPlanWarnsAboutAStandingOptIn(t *testing.T) {
	ctx := context.Background()
	r := &lagResource{src: &switchRegistry{def: &fakeClient{ports: lagPorts()}}}
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	objType := sch.Schema.Type().TerraformType(ctx)
	state := tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)}
	if d := state.Set(ctx, lagState(true, 4)); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: sch.Schema, Raw: state.Raw} // an update or a no-op: the plan is the object
	var resp resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)
	if len(resp.Diagnostics.Warnings()) != 0 {
		t.Fatalf("a plan that keeps the object must not warn about its destroy: %v", warningsOf(resp.Diagnostics))
	}
	empty := tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)} // a create: no state
	var resp2 resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)}, State: empty}, &resp2)
	if len(resp2.Diagnostics.Warnings()) != 0 {
		t.Fatalf("nothing to destroy: %v", warningsOf(resp2.Diagnostics))
	}
}

// The same warning for the other three resources whose delete acts on the switch.
func TestADestroyPlanWarnsForEveryResourceWhoseDeleteActsOnTheSwitch(t *testing.T) {
	reg := func() *switchRegistry { return &switchRegistry{def: newVLANFake("802.1q")} }
	linked := model.MustPortSet(4)
	tests := []struct {
		name string
		warn func(t *testing.T, allow bool) []string
	}{
		{"port-based VLAN group", func(t *testing.T, allow bool) []string {
			t.Helper()
			m := portVLANResourceModel{Switch: types.StringNull(), VLAN: types.Int64Value(2), Ports: portsToSet(linked), AllowDisruptive: types.BoolValue(allow)}
			return warningsOf(destroyPlan(t, &portVLANResource{src: reg()}, m))
		}},
		{"802.1Q VLAN", func(t *testing.T, allow bool) []string {
			t.Helper()
			m := vlan8021QGroupModel{Switch: types.StringNull(), VLAN: types.Int64Value(20), Name: types.StringValue("x"), TaggedPorts: portsToSet(0), UntaggedPorts: portsToSet(linked), AllowDisruptive: types.BoolValue(allow)}
			return warningsOf(destroyPlan(t, &vlan8021QGroupResource{src: reg()}, m))
		}},
		{"PVID", func(t *testing.T, allow bool) []string {
			t.Helper()
			m := vlan8021QPVIDModel{Switch: types.StringNull(), Port: types.Int64Value(4), PVID: types.Int64Value(20), AllowDisruptive: types.BoolValue(allow)}
			return warningsOf(destroyPlan(t, &vlan8021QPVIDResource{src: reg()}, m))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.warn(t, true); len(got) != 1 || !strings.Contains(got[0], "port 4 has a link up") {
				t.Fatalf("want a warning about port 4, got %v", got)
			}
			if got := tt.warn(t, false); len(got) != 0 {
				t.Fatalf("without the stored flag the destroy is guarded: %v", got)
			}
		})
	}
}

// A state that cannot be decoded is reported, not guessed at.
func TestADestroyPlanReportsAnUndecodableState(t *testing.T) {
	ctx := context.Background()
	r := &lagResource{src: &switchRegistry{def: &fakeClient{ports: lagPorts()}}}
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	objType := sch.Schema.Type().TerraformType(ctx)
	req := resource.ModifyPlanRequest{
		Plan:  tfsdk.Plan{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)},
		State: tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(tftypes.String, "not an object")},
	}
	var resp resource.ModifyPlanResponse
	r.ModifyPlan(ctx, req, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("an undecodable state must be reported")
	}
}
