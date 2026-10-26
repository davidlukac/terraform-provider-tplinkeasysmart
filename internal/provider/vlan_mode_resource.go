package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

const vlanModeID = "vlan_mode"

// writableVLANModes lists the modes the library can set, as the attribute spells them.
func writableVLANModes() []string {
	modes := escp.VLANModes()
	names := make([]string, len(modes))
	for i, m := range modes {
		names[i] = string(m)
	}
	return names
}

// vlanModeChoices words the list of modes for the attribute description.
func vlanModeChoices() string {
	names := writableVLANModes()
	return "One of `" + strings.Join(names[:len(names)-1], "`, `") + "` or `" + names[len(names)-1] + "`."
}

// vlanModeMTU is the one mode the resource treats differently (it needs an uplink port). The
// three modes exclude each other: the vendor utility warns that switching discards the settings
// of the other modes. The spellings are the library's (they are part of the Terraform state).
const vlanModeMTU = string(escp.VLANModeMTU)

var (
	_ resource.Resource                = &vlanModeResource{}
	_ resource.ResourceWithConfigure   = &vlanModeResource{}
	_ resource.ResourceWithImportState = &vlanModeResource{}
)

// NewVLANModeResource returns the tplinkeasysmart_vlan_mode resource.
func NewVLANModeResource() resource.Resource { return &vlanModeResource{} }

type vlanModeResource struct {
	src    switchSource
	client vlanModeClient // set on the per-call copy made by bound()
	sw     types.String   // the switch that copy is bound to, for ids
}

// vlanModeClient is the part of the switch API this resource needs.
type vlanModeClient interface {
	VLANMode(ctx context.Context) (escp.VLANModeSetting, error)
	SetVLANMode(ctx context.Context, s escp.VLANModeSetting) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *vlanModeResource) bound(add func(string, string), name types.String) *vlanModeResource {
	return bindSwitch(add, r.src, name, r, func(b *vlanModeResource, c vlanModeClient, sw types.String) { b.client, b.sw = c, sw })
}

type vlanModeModel struct {
	Switch          types.String `tfsdk:"switch"`
	ID              types.String `tfsdk:"id"`
	Mode            types.String `tfsdk:"mode"`
	MTUUplinkPort   types.Int64  `tfsdk:"mtu_uplink_port"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *vlanModeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_mode"
}

func (r *vlanModeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The VLAN mode of the switch (VLAN > MTU VLAN, Port Based VLAN, 802.1Q VLAN): exactly one of the three is active. Singleton; import with id `vlan_mode`. " +
			"**Safety:** switching the mode discards the VLAN settings of the other modes and can cut every connection, including the management path, so the provider " +
			"refuses to change the mode unless `allow_disruptive = true`. Applying the mode the switch already has writes nothing. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Always `vlan_mode`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"mode": schema.StringAttribute{Required: true, Description: vlanModeChoices(),
				Validators: []validator.String{stringvalidator.OneOf(writableVLANModes()...)}},
			"mtu_uplink_port": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{validPortNumber()},
				Description: "The uplink port of the MTU VLAN: every other port can only talk to it. Required with `mode = \"mtu\"`, not allowed with another mode."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow changing the VLAN mode. Defaults to false."),
		},
	}
}

func (r *vlanModeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *vlanModeResource) apply(ctx context.Context, m *vlanModeModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *vlanModeResource) applyBound(ctx context.Context, m *vlanModeModel, add func(string, string)) bool {
	want := escp.VLANModeSetting{Mode: escp.VLANMode(m.Mode.ValueString()), UplinkPort: escp.PortID(m.MTUUplinkPort.ValueInt64())}
	have, err := r.client.VLANMode(ctx)
	if err != nil {
		add("Reading the VLAN mode failed", err.Error())
		return false
	}
	if have != want {
		if !m.AllowDisruptive.ValueBool() {
			add("Refusing to change the VLAN mode", fmt.Sprintf("the switch is in VLAN mode %q and %q was requested. Switching the mode discards the other "+
				"modes' VLAN settings and can cut every connection, including the management path; set allow_disruptive = true to proceed", have.Mode, want.Mode))
			return false
		}
		if err := r.client.SetVLANMode(ctx, want); err != nil {
			add("Setting the VLAN mode failed", err.Error())
			return false
		}
		got, err := r.client.VLANMode(ctx)
		if err != nil || got != want {
			add("The VLAN mode did not change as requested", fmt.Sprintf("requested %+v, the switch reports %+v (err=%v)", want, got, err))
			return false
		}
	}
	m.ID = qualifiedID(r.sw, vlanModeID)
	return true
}

func (r *vlanModeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *vlanModeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *vlanModeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *vlanModeResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vlanModeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	have, err := r.client.VLANMode(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading the VLAN mode failed", err.Error())
		return
	}
	state.ID, state.Mode, state.MTUUplinkPort = qualifiedID(r.sw, vlanModeID), types.StringValue(string(have.Mode)), types.Int64Null()
	if have.Mode == escp.VLANModeMTU {
		state.MTUUplinkPort = types.Int64Value(int64(have.UplinkPort))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vlanModeResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *vlanModeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importSingletonID(ctx, req, resp, vlanModeID) {
		resetAllowDisruptive(ctx, resp)
	}
}

// ValidateConfig rejects, at plan time, an MTU VLAN without its uplink port and an uplink port with another mode.
func (r *vlanModeResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m vlanModeModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if m.Mode.IsUnknown() || m.MTUUplinkPort.IsUnknown() {
		return
	}
	switch {
	case m.Mode.ValueString() == vlanModeMTU && m.MTUUplinkPort.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("mtu_uplink_port"), "MTU VLAN needs an uplink port", `mode = "mtu" requires mtu_uplink_port`)
	case m.Mode.ValueString() != vlanModeMTU && !m.MTUUplinkPort.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("mtu_uplink_port"), "Uplink port without MTU VLAN", `mtu_uplink_port is only used with mode = "mtu"`)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *vlanModeResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var (
	_ resource.ResourceWithValidateConfig = &vlanModeResource{}
	_ resource.ResourceWithModifyPlan     = &vlanModeResource{}
)
