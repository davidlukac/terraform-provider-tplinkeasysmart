package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

var (
	_ resource.Resource                = &portResource{}
	_ resource.ResourceWithConfigure   = &portResource{}
	_ resource.ResourceWithImportState = &portResource{}
)

// NewPortResource returns the tplinkeasysmart_port resource.
func NewPortResource() resource.Resource { return &portResource{} }

type portResource struct{ client SwitchClient }

type portResourceModel struct {
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
			"because that can cut the uplink or the management path; set `allow_disruptive = true` to override. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			"port": schema.Int64Attribute{Required: true, Description: "Port number (1-based).",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(true), Description: "Administrative state. Defaults to true."},
			"speed": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("auto"),
				Description: "One of: " + joinSpeeds() + ". Defaults to auto."},
			"flow_control": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Flow control. Defaults to false."},
			"allow_disruptive": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Allow changing a port that has a link up or is a LAG member. Defaults to false."},
			"actual_speed": schema.StringAttribute{Computed: true, Description: "Currently negotiated speed, or link-down."},
		},
	}
}

func joinSpeeds() string {
	out := ""
	for i, s := range escp.SpeedNames {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func (r *portResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *portResource) find(ctx context.Context, id int) (escp.Port, bool, error) {
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

func (r *portResource) apply(ctx context.Context, m *portResourceModel, add func(string, string)) bool {
	id := int(m.Port.ValueInt64())
	if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), id); err != nil {
		add("Refusing to change port", err.Error())
		return false
	}
	if err := r.client.SetPort(ctx, id, m.Enabled.ValueBool(), m.Speed.ValueString(), m.FlowControl.ValueBool()); err != nil {
		add("Setting port failed", err.Error())
		return false
	}
	got, ok, err := r.find(ctx, id)
	if err != nil || !ok {
		add("Reading port back failed", fmt.Sprintf("port %d: found=%t err=%v", id, ok, err))
		return false
	}
	if got.Enabled != m.Enabled.ValueBool() || got.ConfiguredSpeed != m.Speed.ValueString() || got.ConfiguredFlowControl != m.FlowControl.ValueBool() {
		add("Port did not change as requested", fmt.Sprintf("switch reports enabled=%t speed=%s flow_control=%t", got.Enabled, got.ConfiguredSpeed, got.ConfiguredFlowControl))
		return false
	}
	m.ActualSpeed = types.StringValue(got.ActualSpeed)
	return true
}

func (r *portResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan portResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *portResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan portResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *portResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, ok, err := r.find(ctx, int(state.Port.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading port failed", err.Error())
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Enabled, state.Speed = types.BoolValue(got.Enabled), types.StringValue(got.ConfiguredSpeed)
	state.FlowControl, state.ActualSpeed = types.BoolValue(got.ConfiguredFlowControl), types.StringValue(got.ActualSpeed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *portResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *portResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.Atoi(req.ID)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a port number, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("port"), int64(n))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_disruptive"), false)...)
}
