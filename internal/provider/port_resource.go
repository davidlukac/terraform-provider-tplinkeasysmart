package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

var (
	_ resource.Resource                   = &portResource{}
	_ resource.ResourceWithConfigure      = &portResource{}
	_ resource.ResourceWithImportState    = &portResource{}
	_ resource.ResourceWithValidateConfig = &portResource{}
)

// NewPortResource returns the tplinkeasysmart_port resource.
func NewPortResource() resource.Resource { return &portResource{} }

type portResource struct {
	src    switchSource
	client portClient   // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// portClient is the part of the switch API this resource needs.
type portClient interface {
	Ports(ctx context.Context) ([]escp.Port, error)
	SetPort(ctx context.Context, cfg escp.PortConfig) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *portResource) bound(add func(string, string), name types.String) *portResource {
	return bindSwitch(add, r.src, name, r, func(b *portResource, c portClient, sw types.String) { b.client, b.sw = c, sw })
}

type portResourceModel struct {
	Switch          types.String `tfsdk:"switch"`
	Port            types.Int64  `tfsdk:"port"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	Speed           types.String `tfsdk:"speed"`
	FlowControl     types.Bool   `tfsdk:"flow_control"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
	ActualSpeed     types.String `tfsdk:"actual_speed"`
}

func (r *portResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port"
}

func (r *portResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Configuration of one physical port (Switching > Port Setting): admin state, speed and flow control. " +
			"Import with the port number. **Safety:** by default the provider refuses to change a port that has a link up or is in a LAG, " +
			"because that can cut the uplink or the management path; set `allow_disruptive = true` to override. On a LAG member the switch refuses half duplex " +
			"but does not keep the members' speed, flow control or enabled state alike, so change all members together. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"port": schema.Int64Attribute{Required: true, Description: "Port number (1-based).",
				Validators:    []validator.Int64{validPortNumber()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Administrative state. Defaults to true."},
			"speed": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("auto"),
				Validators: []validator.String{validSpeed()}, Description: "One of: " + joinSpeeds() + ". Defaults to auto."},
			"flow_control": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Flow control. Defaults to false. It can be true only with speed `auto` or `1000Mbps-full`: at a fixed 10 or 100 Mbps speed the switch accepts the setting but keeps flow control off, so that combination is refused while planning."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow changing a port that has a link up or is a LAG member. Defaults to false."),
			"actual_speed":      schema.StringAttribute{Computed: true, Description: "Currently negotiated speed, or link-down."},
		},
	}
}

func joinSpeeds() string {
	out := ""
	for i, s := range speedNames() {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func (r *portResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *portResource) find(ctx context.Context, id escp.PortID) (escp.Port, bool, error) {
	ports, err := r.client.Ports(ctx)
	if err != nil {
		return escp.Port{}, false, err
	}
	for _, p := range ports {
		if p.ID == id {
			return p, true, nil
		}
	}
	return escp.Port{}, false, nil
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *portResource) apply(ctx context.Context, m *portResourceModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *portResource) applyBound(ctx context.Context, m *portResourceModel, add func(string, string)) bool {
	id := escp.PortID(m.Port.ValueInt64())
	if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), id); err != nil {
		add("Refusing to change port", err.Error())
		return false
	}
	if err := r.client.SetPort(ctx, escp.PortConfig{ID: id, Enabled: m.Enabled.ValueBool(), Speed: escp.Speed(m.Speed.ValueString()), FlowControl: m.FlowControl.ValueBool()}); err != nil {
		add("Setting port failed", err.Error())
		return false
	}
	got, ok, err := r.find(ctx, id)
	if err != nil || !ok {
		add("Reading port back failed", fmt.Sprintf("port %d: found=%t err=%v", id, ok, err))
		return false
	}
	if got.Enabled != m.Enabled.ValueBool() || got.ConfiguredSpeed.String() != m.Speed.ValueString() || got.ConfiguredFlowControl != m.FlowControl.ValueBool() {
		add("Port did not change as requested", fmt.Sprintf("switch reports enabled=%t speed=%s flow_control=%t", got.Enabled, got.ConfiguredSpeed, got.ConfiguredFlowControl))
		return false
	}
	m.ActualSpeed = types.StringValue(got.ActualSpeed.String())
	return true
}

func (r *portResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *portResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyUpdate(ctx, req, resp, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *portResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *portResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, ok, err := r.find(ctx, escp.PortID(state.Port.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading port failed", err.Error())
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Enabled, state.Speed = types.BoolValue(got.Enabled), types.StringValue(got.ConfiguredSpeed.String())
	state.FlowControl, state.ActualSpeed = types.BoolValue(got.ConfiguredFlowControl), types.StringValue(got.ActualSpeed.String())
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *portResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *portResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a port number", "port") {
		resetAllowDisruptive(ctx, resp)
	}
}

// portFlowControlProblem says why a configuration asks for flow control at a speed that cannot
// hold it, or returns "" when it is fine. An omitted speed or flow control takes its schema
// default (auto, false); a value that is not known yet, or a speed the speed validator rejects
// anyway, is not judged here.
func portFlowControlProblem(speed types.String, flow types.Bool) string {
	if speed.IsUnknown() || flow.IsUnknown() || !flow.ValueBool() {
		return ""
	}
	s := escp.SpeedAuto
	if !speed.IsNull() {
		parsed, err := escp.ParseSpeed(speed.ValueString())
		if err != nil {
			return ""
		}
		s = parsed
	}
	if s.AllowsFlowControl() {
		return ""
	}
	return fmt.Sprintf("flow_control = true needs speed = %q or %q, not %q. The switch accepts the setting but keeps flow control off at a fixed 10 or 100 Mbps speed, so the port would be left changed and the apply would then fail; the vendor utility refuses the same combination.", escp.SpeedAuto, escp.Speed1000Full, s)
}

// ValidateConfig rejects, at plan time, flow control together with a fixed 10 or 100 Mbps speed.
func (r *portResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m portResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if problem := portFlowControlProblem(m.Speed, m.FlowControl); problem != "" {
		resp.Diagnostics.AddAttributeError(path.Root("flow_control"), "Invalid port configuration", problem)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *portResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &portResource{}
