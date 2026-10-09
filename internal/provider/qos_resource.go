package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

const qosID = "qos"

var (
	_ resource.Resource                = &qosResource{}
	_ resource.ResourceWithConfigure   = &qosResource{}
	_ resource.ResourceWithImportState = &qosResource{}
)

// NewQoSResource returns the tplinkeasysmart_qos resource.
func NewQoSResource() resource.Resource { return &qosResource{} }

type qosResource struct{ client qosClient }

// qosClient is the part of the switch API this resource needs.
type qosClient interface {
	QoS(ctx context.Context) (escp.QoS, error)
	SetPortPriorities(ctx context.Context, changes map[int]int) error
	SetQoSMode(ctx context.Context, mode int) error
}

type qosResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Mode       types.String `tfsdk:"mode"`
	Priorities types.Map    `tfsdk:"port_priorities"`
}

func (r *qosResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_qos"
}

func (r *qosResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "QoS basic settings (QoS > QoS Basic). Singleton; import with id `qos`. Changing QoS only reorders queues under congestion and cannot cut connectivity. " +
			"Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `qos`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"mode": schema.StringAttribute{Required: true, Validators: []validator.String{validQoSMode()}, Description: "port-based, 802.1p or dscp-802.1p."},
			"port_priorities": schema.MapAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Validators: validPriorities(),
				Default: mapdefault.StaticValue(types.MapValueMust(types.Int64Type, map[string]attr.Value{})),
				Description: "Priority queue per port, keyed by port number (\"7\" = 2), 1 (lowest) to 4 (highest). Only the listed ports are managed. " +
					"Priorities take effect only when mode is port-based; setting them in another mode is rejected."},
		},
	}
}

func (r *qosResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom[qosClient](req.ProviderData, &resp.Diagnostics)
}

func modeCode(name string) (int, bool) {
	for code, n := range escp.QoSModeNames {
		if n == name {
			return code, true
		}
	}
	return 0, false
}

// qosState builds the model from the switch, keeping only the priority keys we manage.
func qosState(q escp.QoS, managed map[string]struct{}) qosResourceModel {
	vals := map[string]attr.Value{}
	for _, p := range q.Priorities {
		k := strconv.Itoa(p.Port)
		if _, ok := managed[k]; ok {
			vals[k] = types.Int64Value(int64(p.Priority))
		}
	}
	// every element is an Int64 value, so the map cannot violate its element type
	return qosResourceModel{ID: types.StringValue(qosID), Mode: types.StringValue(q.ModeName),
		Priorities: types.MapValueMust(types.Int64Type, vals)}
}

func (r *qosResource) apply(ctx context.Context, plan *qosResourceModel, add func(string, string)) bool {
	code, ok := modeCode(plan.Mode.ValueString())
	if !ok {
		add("Invalid QoS mode", fmt.Sprintf("%q is not one of port-based, 802.1p, dscp-802.1p", plan.Mode.ValueString()))
		return false
	}
	want := map[int]int{}
	managed := map[string]struct{}{}
	for k, v := range plan.Priorities.Elements() {
		port, err := strconv.Atoi(k)
		if err != nil {
			add("Invalid port key", fmt.Sprintf("port_priorities key %q is not a port number", k))
			return false
		}
		want[port] = int(v.(types.Int64).ValueInt64())
		managed[k] = struct{}{}
	}
	if problem := qosProblem(plan.Mode, plan.Priorities); problem != "" {
		add("Priorities need port-based mode", problem)
		return false
	}
	if err := r.client.SetQoSMode(ctx, code); err != nil {
		add("Setting QoS mode failed", err.Error())
		return false
	}
	if len(want) > 0 {
		if err := r.client.SetPortPriorities(ctx, want); err != nil {
			add("Setting port priorities failed", err.Error())
			return false
		}
	}
	got, err := r.client.QoS(ctx)
	if err != nil {
		add("Reading QoS back failed", err.Error())
		return false
	}
	if got.Mode != code {
		add("QoS mode did not change", fmt.Sprintf("requested %s, switch reports %s", plan.Mode.ValueString(), got.ModeName))
		return false
	}
	for _, p := range got.Priorities {
		if w, ok := want[p.Port]; ok && w != p.Priority {
			add("Port priority did not change", fmt.Sprintf("port %d: requested %d, switch reports %d", p.Port, w, p.Priority))
			return false
		}
	}
	*plan = qosState(got, managed)
	return true
}

func (r *qosResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *qosResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *qosResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state qosResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	managed := map[string]struct{}{}
	for k := range state.Priorities.Elements() {
		managed[k] = struct{}{}
	}
	q, err := r.client.QoS(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading QoS failed", err.Error())
		return
	}
	m := qosState(q, managed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *qosResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *qosResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingletonID(ctx, req, resp, qosID)
}

// ValidateConfig rejects, at plan time, per-port priorities outside port-based mode.
func (r *qosResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m qosResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if problem := qosProblem(m.Mode, m.Priorities); problem != "" {
		resp.Diagnostics.AddAttributeError(path.Root("port_priorities"), "Priorities need port-based mode", problem)
	}
}

var _ resource.ResourceWithValidateConfig = &qosResource{}
