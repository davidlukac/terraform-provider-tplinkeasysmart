package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

var (
	_ resource.Resource                = &stormLimitResource{}
	_ resource.ResourceWithConfigure   = &stormLimitResource{}
	_ resource.ResourceWithImportState = &stormLimitResource{}
)

// NewStormLimitResource returns the tplinkeasysmart_storm_limit resource.
func NewStormLimitResource() resource.Resource { return &stormLimitResource{} }

type stormLimitResource struct {
	src    switchSource
	client stormLimitClient // set on the per-call copy made by bound()
	sw     types.String     // the switch that copy is bound to, for ids
}

// stormLimitClient is the part of the switch API this resource needs.
type stormLimitClient interface {
	Ports(ctx context.Context) ([]escp.Port, error)
	SetStormControl(ctx context.Context, cfg escp.StormConfig) error
	StormControl(ctx context.Context) ([]escp.PortStorm, error)
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *stormLimitResource) bound(add func(string, string), name types.String) *stormLimitResource {
	return bindSwitch(add, r.src, name, r, func(b *stormLimitResource, c stormLimitClient, sw types.String) { b.client, b.sw = c, sw })
}

type stormLimitModel struct {
	Switch          types.String `tfsdk:"switch"`
	Port            types.Int64  `tfsdk:"port"`
	UnknownUnicast  types.Bool   `tfsdk:"unknown_unicast"`
	Multicast       types.Bool   `tfsdk:"multicast"`
	Broadcast       types.Bool   `tfsdk:"broadcast"`
	RateKbps        types.Int64  `tfsdk:"rate_kbps"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *stormLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storm_limit"
}

func (r *stormLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Storm control of one port (QoS > Storm Control). Selecting traffic types requires a rate (" + stormRateRangeText() + "); selecting none disables it with rate 0. Import with the port number. **Safety:** a rate drops excess traffic of the chosen types, so by default " +
			"the provider refuses to change a port that has a link up or is in a LAG; set `allow_disruptive = true` to override. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"port": schema.Int64Attribute{Required: true, Description: "Port number (1-based).",
				Validators:    []validator.Int64{validPortNumber()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"unknown_unicast": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Limit unknown unicast traffic."},
			"multicast":       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Limit multicast traffic."},
			"broadcast":       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Limit broadcast traffic."},
			"rate_kbps": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{rateKbps{}},
				Description: fmt.Sprintf("Rate in kbps (multiple of %d, %d..%d) when a traffic type is selected, otherwise 0.", escp.RateStepKbps, escp.RateStepKbps, escp.MaxBandwidthKbps)},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow changing a port that has a link up or is a LAG member. Defaults to false."),
		},
	}
}

func (r *stormLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *stormLimitResource) find(ctx context.Context, port int) (escp.PortStorm, bool, error) {
	rows, err := r.client.StormControl(ctx)
	if err != nil {
		return escp.PortStorm{}, false, err
	}
	for _, row := range rows {
		if row.Port == port {
			return row, true, nil
		}
	}
	return escp.PortStorm{}, false, nil
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *stormLimitResource) apply(ctx context.Context, m *stormLimitModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *stormLimitResource) applyBound(ctx context.Context, m *stormLimitModel, add func(string, string)) bool {
	port := int(m.Port.ValueInt64())
	uu, mc, bc, rate := m.UnknownUnicast.ValueBool(), m.Multicast.ValueBool(), m.Broadcast.ValueBool(), int(m.RateKbps.ValueInt64())
	if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), port); err != nil {
		add("Refusing to change port", err.Error())
		return false
	}
	if err := r.client.SetStormControl(ctx, escp.StormConfig{Port: port, UnknownUnicast: uu, Multicast: mc, Broadcast: bc, RateKbps: rate}); err != nil {
		add("Setting storm control failed", err.Error())
		return false
	}
	got, ok, err := r.find(ctx, port)
	if err != nil || !ok || got.UnknownUnicast != uu || got.Multicast != mc || got.Broadcast != bc || got.RateKbps != rate {
		add("Storm control did not change as requested", fmt.Sprintf("requested uu=%t mc=%t bc=%t rate=%d, switch reports %+v (found=%t err=%v)", uu, mc, bc, rate, got, ok, err))
		return false
	}
	return true
}

func (r *stormLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *stormLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyUpdate(ctx, req, resp, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *stormLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *stormLimitResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state stormLimitModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, ok, err := r.find(ctx, int(state.Port.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading storm control failed", err.Error())
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.UnknownUnicast, state.Multicast, state.Broadcast = types.BoolValue(got.UnknownUnicast), types.BoolValue(got.Multicast), types.BoolValue(got.Broadcast)
	state.RateKbps = types.Int64Value(int64(got.RateKbps))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *stormLimitResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *stormLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a port number", "port") {
		resetAllowDisruptive(ctx, resp)
	}
}

// ValidateConfig rejects, at plan time, a rate without a traffic type and a traffic type without a rate.
func (r *stormLimitResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m stormLimitModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	// Omitted attributes are null here and take their schema defaults (false, 0) at plan time.
	if problem := stormProblem(defaultFalse(m.UnknownUnicast), defaultFalse(m.Multicast), defaultFalse(m.Broadcast), defaultZero(m.RateKbps)); problem != "" {
		resp.Diagnostics.AddAttributeError(path.Root("rate_kbps"), "Invalid storm control configuration", problem)
	}
}

var _ resource.ResourceWithValidateConfig = &stormLimitResource{}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *stormLimitResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &stormLimitResource{}
