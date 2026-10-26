package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// defaultPVID is the PVID every port has until it is changed, and the one a destroyed resource restores.
const defaultPVID = 1

var (
	_ resource.Resource                = &vlan8021QPVIDResource{}
	_ resource.ResourceWithConfigure   = &vlan8021QPVIDResource{}
	_ resource.ResourceWithImportState = &vlan8021QPVIDResource{}
)

// NewVLAN8021QPVIDResource returns the tplinkeasysmart_vlan_8021q_pvid resource.
func NewVLAN8021QPVIDResource() resource.Resource { return &vlan8021QPVIDResource{} }

type vlan8021QPVIDResource struct {
	src    switchSource
	client vlan8021QPVIDClient // set on the per-call copy made by bound()
	sw     types.String        // the switch that copy is bound to, for ids
}

// vlan8021QPVIDClient is the part of the switch API this resource needs.
type vlan8021QPVIDClient interface {
	Dot1Q(ctx context.Context) (escp.Dot1Q, error)
	SetPVIDs(ctx context.Context, pvids map[int]int) error
	Ports(ctx context.Context) ([]escp.Port, error)
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *vlan8021QPVIDResource) bound(add func(string, string), name types.String) *vlan8021QPVIDResource {
	return bindSwitch(add, r.src, name, r, func(b *vlan8021QPVIDResource, c vlan8021QPVIDClient, sw types.String) { b.client, b.sw = c, sw })
}

type vlan8021QPVIDModel struct {
	Switch          types.String `tfsdk:"switch"`
	Port            types.Int64  `tfsdk:"port"`
	PVID            types.Int64  `tfsdk:"pvid"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *vlan8021QPVIDResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_8021q_pvid"
}

func (r *vlan8021QPVIDResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The PVID of one port (VLAN > 802.1Q PVID Setting): the VLAN that untagged traffic entering the port is assigned to. Import with the port number. " +
			"The 802.1Q mode must be active and the VLAN must exist (see `tplinkeasysmart_vlan_mode` and `tplinkeasysmart_vlan_8021q_group`). " +
			"**Safety:** the provider refuses to change a port that has a link up or is in a LAG, because a wrong PVID drops the port's untagged traffic; " +
			"set `allow_disruptive = true` to override. Destroying sets the PVID back to 1 (when 802.1Q is active and VLAN 1 exists), under the same guard.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"port": schema.Int64Attribute{Required: true, Description: "Port number (1-based).",
				Validators:    []validator.Int64{validPortNumber()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"pvid":              schema.Int64Attribute{Required: true, Validators: []validator.Int64{validVLANID()}, Description: "The port's default VLAN id, " + vlanIDRangeText() + "."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow changing a port that has a link up or is a LAG member. Defaults to false."),
		},
	}
}

func (r *vlan8021QPVIDResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// state reads the 802.1Q state; the mode must be active, because the switch does not apply PVIDs otherwise.
func (r *vlan8021QPVIDResource) state(ctx context.Context) (escp.Dot1Q, error) {
	q, err := r.client.Dot1Q(ctx)
	if err != nil {
		return q, err
	}
	if !q.Enabled {
		return q, &inactiveModeError{"802.1Q VLAN mode is not active on the switch; refusing to manage PVIDs in another VLAN mode (see tplinkeasysmart_vlan_mode)"}
	}
	return q, nil
}

// pvidOf returns the PVID of a port, if the switch reports one.
func pvidOf(q escp.Dot1Q, port int) (int, bool) {
	for _, p := range q.PVIDs {
		if p.Port == port {
			return p.PVID, true
		}
	}
	return 0, false
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *vlan8021QPVIDResource) apply(ctx context.Context, m *vlan8021QPVIDModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *vlan8021QPVIDResource) applyBound(ctx context.Context, m *vlan8021QPVIDModel, add func(string, string)) bool {
	port, pvid := int(m.Port.ValueInt64()), int(m.PVID.ValueInt64())
	return r.set(ctx, port, pvid, m.AllowDisruptive.ValueBool(), add)
}

// set writes the PVID of a port under the guard and checks it by reading it back.
func (r *vlan8021QPVIDResource) set(ctx context.Context, port, pvid int, allow bool, add func(string, string)) bool {
	q, err := r.state(ctx)
	if err != nil {
		add("Reading the 802.1Q state failed", err.Error())
		return false
	}
	if _, ok := findVLAN(q.VLANs, pvid); !ok {
		add("The VLAN does not exist", fmt.Sprintf("VLAN %d is not defined on the switch; create it first (tplinkeasysmart_vlan_8021q_group)", pvid))
		return false
	}
	if err := guardPorts(ctx, r.client, allow, port); err != nil {
		add("Refusing to change the PVID", err.Error())
		return false
	}
	if err := r.client.SetPVIDs(ctx, map[int]int{port: pvid}); err != nil {
		add("Setting the PVID failed", err.Error())
		return false
	}
	after, err := r.state(ctx)
	if got, ok := pvidOf(after, port); err != nil || !ok || got != pvid {
		add("The PVID did not change as requested", fmt.Sprintf("requested %d on port %d, the switch reports %d (found=%t, err=%v)", pvid, port, got, ok, err))
		return false
	}
	return true
}

func (r *vlan8021QPVIDResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *vlan8021QPVIDResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyUpdate(ctx, req, resp, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *vlan8021QPVIDResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *vlan8021QPVIDResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vlan8021QPVIDModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	q, err := r.state(ctx)
	if err != nil {
		if !warnInactiveMode(&resp.Diagnostics, err, "the PVIDs", "The state of this resource is kept as it is and cannot be checked until the mode is switched back; changes are refused meanwhile") {
			resp.Diagnostics.AddError("Reading the 802.1Q state failed", err.Error())
		}
		return
	}
	pvid, ok := pvidOf(q, int(state.Port.ValueInt64()))
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.PVID = types.Int64Value(int64(pvid))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// deleteBound sets the PVID back to the default when that is possible.
func (r *vlan8021QPVIDResource) deleteBound(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vlan8021QPVIDModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	port := int(state.Port.ValueInt64())
	q, err := r.client.Dot1Q(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading the 802.1Q state failed", err.Error())
		return
	}
	pvid, _ := pvidOf(q, port)
	if !q.Enabled {
		resp.Diagnostics.AddWarning("The VLAN mode of the PVIDs is not active", "802.1Q VLAN mode is not active on the switch, so the PVID cannot be put back; the resource is only forgotten and the switch is left as it is")
		return
	}
	if _, hasDefault := findVLAN(q.VLANs, defaultPVID); !hasDefault || pvid == defaultPVID {
		return // nothing to restore: VLAN 1 is gone, or the port already uses it
	}
	r.set(ctx, port, defaultPVID, state.AllowDisruptive.ValueBool(), resp.Diagnostics.AddError)
}

// Delete binds the resource to the switch recorded in state, then resets the PVID.
func (r *vlan8021QPVIDResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if b := r.bound(resp.Diagnostics.AddError, switchOf(ctx, req.State, &resp.Diagnostics)); b != nil {
		b.deleteBound(ctx, req, resp)
	}
}

func (r *vlan8021QPVIDResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a port number", "port") {
		resetAllowDisruptive(ctx, resp)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *vlan8021QPVIDResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &vlan8021QPVIDResource{}
