package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &bandwidthLimitResource{}
	_ resource.ResourceWithConfigure   = &bandwidthLimitResource{}
	_ resource.ResourceWithImportState = &bandwidthLimitResource{}
)

// NewBandwidthLimitResource returns the tplinkeasysmart_bandwidth_limit resource.
func NewBandwidthLimitResource() resource.Resource { return &bandwidthLimitResource{} }

type bandwidthLimitResource struct {
	src    switchSource
	client bandwidthLimitClient // set on the per-call copy made by bound()
	sw     types.String         // the switch that copy is bound to, for ids
}

// bandwidthLimitClient is the part of the switch API this resource needs.
type bandwidthLimitClient interface {
	Bandwidth(ctx context.Context) ([]escp.PortBandwidth, error)
	Ports(ctx context.Context) ([]escp.Port, error)
	SetBandwidth(ctx context.Context, port, ingressKbps, egressKbps int) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *bandwidthLimitResource) bound(add func(string, string), name types.String) *bandwidthLimitResource {
	return bindSwitch(add, r.src, name, r, func(b *bandwidthLimitResource, c bandwidthLimitClient, sw types.String) { b.client, b.sw = c, sw })
}

type bandwidthLimitModel struct {
	Switch          types.String `tfsdk:"switch"`
	Port            types.Int64  `tfsdk:"port"`
	IngressKbps     types.Int64  `tfsdk:"ingress_kbps"`
	EgressKbps      types.Int64  `tfsdk:"egress_kbps"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *bandwidthLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bandwidth_limit"
}

func (r *bandwidthLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Rate limits of one port (QoS > Bandwidth Control). Import with the port number. **Safety:** a limit throttles the port, so by default " +
			"the provider refuses to change a port that has a link up or is in a LAG; set `allow_disruptive = true` to override. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"port": schema.Int64Attribute{Required: true, Description: "Port number (1-based).",
				Validators:    []validator.Int64{validPortNumber()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"ingress_kbps": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{rateKbps{}},
				Description: "Ingress limit in kbps: 0 (unlimited) or a multiple of 64 up to 1000000."},
			"egress_kbps": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{rateKbps{}},
				Description: "Egress limit in kbps: 0 (unlimited) or a multiple of 64 up to 1000000."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow changing a port that has a link up or is a LAG member. Defaults to false."),
		},
	}
}

func (r *bandwidthLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *bandwidthLimitResource) find(ctx context.Context, port int) (ingress, egress int, ok bool, err error) {
	rows, err := r.client.Bandwidth(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	for _, row := range rows {
		if row.Port == port {
			return row.IngressKbps, row.EgressKbps, true, nil
		}
	}
	return 0, 0, false, nil
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *bandwidthLimitResource) apply(ctx context.Context, m *bandwidthLimitModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *bandwidthLimitResource) applyBound(ctx context.Context, m *bandwidthLimitModel, add func(string, string)) bool {
	port := int(m.Port.ValueInt64())
	in, eg := int(m.IngressKbps.ValueInt64()), int(m.EgressKbps.ValueInt64())
	if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), port); err != nil {
		add("Refusing to change port", err.Error())
		return false
	}
	if err := r.client.SetBandwidth(ctx, port, in, eg); err != nil {
		add("Setting bandwidth limit failed", err.Error())
		return false
	}
	gi, ge, ok, err := r.find(ctx, port)
	if err != nil || !ok || gi != in || ge != eg {
		add("Bandwidth limit did not change as requested", fmt.Sprintf("requested in=%d out=%d, switch reports in=%d out=%d (found=%t err=%v)", in, eg, gi, ge, ok, err))
		return false
	}
	return true
}

func (r *bandwidthLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *bandwidthLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *bandwidthLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *bandwidthLimitResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state bandwidthLimitModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in, eg, ok, err := r.find(ctx, int(state.Port.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading bandwidth limit failed", err.Error())
		return
	}
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.IngressKbps, state.EgressKbps = types.Int64Value(int64(in)), types.Int64Value(int64(eg))
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *bandwidthLimitResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *bandwidthLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a port number", "port") {
		resetAllowDisruptive(ctx, resp)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *bandwidthLimitResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &bandwidthLimitResource{}
