package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

var (
	_ resource.Resource                = &stormLimitResource{}
	_ resource.ResourceWithConfigure   = &stormLimitResource{}
	_ resource.ResourceWithImportState = &stormLimitResource{}
)

// NewStormLimitResource returns the tplinkeasysmart_storm_limit resource.
func NewStormLimitResource() resource.Resource { return &stormLimitResource{} }

type stormLimitResource struct{ client SwitchClient }

type stormLimitModel struct {
	Port            types.Int64 `tfsdk:"port"`
	UnknownUnicast  types.Bool  `tfsdk:"unknown_unicast"`
	Multicast       types.Bool  `tfsdk:"multicast"`
	Broadcast       types.Bool  `tfsdk:"broadcast"`
	RateKbps        types.Int64 `tfsdk:"rate_kbps"`
	AllowDisruptive types.Bool  `tfsdk:"allow_disruptive"`
}

func (r *stormLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storm_limit"
}

func (r *stormLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Storm control of one port (QoS > Storm Control). Selecting traffic types requires a rate (64..1000000 kbps, multiple of 64); selecting none disables it with rate 0. Import with the port number. **Safety:** a rate drops excess traffic of the chosen types, so by default " +
			"the provider refuses to change a port that has a link up or is in a LAG; set `allow_disruptive = true` to override. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			"port": schema.Int64Attribute{Required: true, Description: "Port number (1-based).",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"unknown_unicast": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Limit unknown unicast traffic."},
			"multicast":       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Limit multicast traffic."},
			"broadcast":       schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Limit broadcast traffic."},
			"rate_kbps": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0),
				Description: "Rate in kbps (multiple of 64, 64..1000000) when a traffic type is selected, otherwise 0."},
			"allow_disruptive": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Allow changing a port that has a link up or is a LAG member. Defaults to false."},
		},
	}
}

func (r *stormLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
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

func (r *stormLimitResource) apply(ctx context.Context, m *stormLimitModel, add func(string, string)) bool {
	port := int(m.Port.ValueInt64())
	uu, mc, bc, rate := m.UnknownUnicast.ValueBool(), m.Multicast.ValueBool(), m.Broadcast.ValueBool(), int(m.RateKbps.ValueInt64())
	if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), port); err != nil {
		add("Refusing to change port", err.Error())
		return false
	}
	if err := r.client.SetStormControl(ctx, port, uu, mc, bc, rate); err != nil {
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
	var plan stormLimitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *stormLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan stormLimitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *stormLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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
	n, err := strconv.Atoi(req.ID)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a port number, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("port"), int64(n))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_disruptive"), false)...)
}
