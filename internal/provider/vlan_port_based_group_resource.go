package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &portVLANResource{}
	_ resource.ResourceWithConfigure   = &portVLANResource{}
	_ resource.ResourceWithImportState = &portVLANResource{}
)

// NewVLANPortBasedGroupResource returns the tplinkeasysmart_lag resource.
func NewVLANPortBasedGroupResource() resource.Resource { return &portVLANResource{} }

type portVLANResource struct {
	src    switchSource
	client portVLANClient // set on the per-call copy made by bound()
	sw     types.String   // the switch that copy is bound to, for ids
}

// portVLANClient is the part of the switch API this resource needs.
type portVLANClient interface {
	PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error)
	Ports(ctx context.Context) ([]escp.Port, error)
	SetPortBasedVLAN(ctx context.Context, id int, ports []int) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *portVLANResource) bound(add func(string, string), name types.String) *portVLANResource {
	return bindSwitch(add, r.src, name, r, func(b *portVLANResource, c portVLANClient, sw types.String) { b.client, b.sw = c, sw })
}

type portVLANResourceModel struct {
	Switch          types.String `tfsdk:"switch"`
	VLAN            types.Int64  `tfsdk:"vlan"`
	Ports           types.Set    `tfsdk:"ports"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *portVLANResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_port_based_group"
}

func (r *portVLANResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A port-based VLAN group (VLAN > Port Based VLAN). Import with the VLAN id. The VLAN **mode** is not managed here. Membership is exclusive: " +
			"adding ports to a group removes them from their previous group, and removing a group returns its ports to VLAN 1. **Safety:** the provider refuses to " +
			"create, change or destroy a group when any port involved (old or new members) has a link up or belongs to a LAG, because that can cut connectivity or " +
			"management access; set `allow_disruptive = true` to override. Import VLAN 1 (the default group) only together with `lifecycle { prevent_destroy = true }`.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"vlan": schema.Int64Attribute{Required: true, Description: "VLAN group id.",
				Validators:    []validator.Int64{validByteID()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"ports":             schema.SetAttribute{Required: true, ElementType: types.Int64Type, Validators: []validator.Set{validPortSet(), setvalidator.SizeAtLeast(1)}, Description: "Member ports (at least one)."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow touching ports that have a link up or belong to a LAG. Defaults to false."),
		},
	}
}

func (r *portVLANResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// guardVLAN checks every port of the old and new membership: none may have a
// link up or belong to a LAG, because VLAN membership is exclusive and a
// change moves the port out of its previous group.
func (r *portVLANResource) guardVLAN(ctx context.Context, allow bool, old, new []int) error {
	if allow {
		tflog.Warn(ctx, "allow_disruptive is set: the port-in-use guard is skipped")
		return nil
	}
	all, err := r.client.Ports(ctx)
	if err != nil {
		return fmt.Errorf("cannot check whether the ports are in use: %w", err)
	}
	seen := map[int]bool{}
	for _, p := range append(append([]int{}, old...), new...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		use := safety.UseOf(all, p)
		switch {
		case !use.Exists:
			return fmt.Errorf("port %d does not exist on this switch", p)
		case use.LinkUp():
			return fmt.Errorf("port %d has a link up (%s); changing its VLAN membership can cut traffic or management access. Set allow_disruptive = true to proceed anyway", p, use.Speed)
		case use.InLAG():
			return fmt.Errorf("port %d is a member of LAG%d; changing its VLAN can cut the uplink. Set allow_disruptive = true to proceed anyway", p, use.LAG)
		}
	}
	return nil
}

func (r *portVLANResource) current(ctx context.Context, group int) ([]int, error) {
	v, err := r.client.PortBasedVLAN(ctx)
	if err != nil {
		return nil, err
	}
	if !v.Enabled {
		return nil, fmt.Errorf("port-based VLAN mode is not active on the switch; refusing to manage groups in another VLAN mode")
	}
	for _, g := range v.VLANs {
		if g.ID == group {
			p := append([]int(nil), g.Ports...)
			sort.Ints(p)
			return p, nil
		}
	}
	return []int{}, nil // group does not exist yet
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *portVLANResource) apply(ctx context.Context, m *portVLANResourceModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *portVLANResource) applyBound(ctx context.Context, m *portVLANResourceModel, add func(string, string)) bool {
	group := int(m.VLAN.ValueInt64())
	ports, err := setToInts(ctx, m.Ports)
	if err != nil || len(ports) == 0 {
		add("Invalid ports", "a VLAN group needs at least one member port")
		return false
	}
	old, err := r.current(ctx, group)
	if err != nil {
		add("Reading VLAN group failed", err.Error())
		return false
	}
	if err := r.guardVLAN(ctx, m.AllowDisruptive.ValueBool(), old, ports); err != nil {
		add("Refusing to change VLAN group", err.Error())
		return false
	}
	if err := r.client.SetPortBasedVLAN(ctx, group, ports); err != nil {
		add("Setting VLAN group failed", err.Error())
		return false
	}
	got, err := r.current(ctx, group)
	if err != nil || fmt.Sprint(got) != fmt.Sprint(ports) {
		add("VLAN group did not change as requested", fmt.Sprintf("requested %v, switch reports %v (err=%v)", ports, got, err))
		return false
	}
	return true
}

func (r *portVLANResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *portVLANResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *portVLANResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *portVLANResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portVLANResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.current(ctx, int(state.VLAN.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading VLAN group failed", err.Error())
		return
	}
	if len(got) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Ports = intsToSet(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete removes the group, under the same guard as every other change.
func (r *portVLANResource) deleteBound(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state portVLANResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group := int(state.VLAN.ValueInt64())
	old, err := r.current(ctx, group)
	if err != nil {
		resp.Diagnostics.AddError("Reading VLAN group failed", err.Error())
		return
	}
	if err := r.guardVLAN(ctx, state.AllowDisruptive.ValueBool(), old, nil); err != nil {
		resp.Diagnostics.AddError("Refusing to remove VLAN group", err.Error())
		return
	}
	if err := r.client.SetPortBasedVLAN(ctx, group, nil); err != nil {
		resp.Diagnostics.AddError("Removing VLAN group failed", err.Error())
	}
}

// Delete binds the resource to the switch recorded in state, then removes it.
func (r *portVLANResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if b := r.bound(resp.Diagnostics.AddError, switchOf(ctx, req.State, &resp.Diagnostics)); b != nil {
		b.deleteBound(ctx, req, resp)
	}
}

func (r *portVLANResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a VLAN id", "vlan") {
		resetAllowDisruptive(ctx, resp)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *portVLANResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &portVLANResource{}
