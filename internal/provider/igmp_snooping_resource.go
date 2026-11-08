package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/escp-go/model"
)

const igmpID = "igmp_snooping"

var (
	_ resource.Resource                = &igmpResource{}
	_ resource.ResourceWithConfigure   = &igmpResource{}
	_ resource.ResourceWithImportState = &igmpResource{}
)

// NewIGMPSnoopingResource returns the tplinkeasysmart_igmp_snooping resource.
func NewIGMPSnoopingResource() resource.Resource { return &igmpResource{} }

type igmpResource struct {
	src    switchSource
	client igmpClient   // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// igmpClient is the part of the switch API this resource needs.
type igmpClient interface {
	IGMP(ctx context.Context) (model.IGMP, error)
	SetIGMP(ctx context.Context, cfg model.IGMPConfig) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *igmpResource) bound(add func(string, string), name types.String) *igmpResource {
	return bindSwitch(add, r.src, name, r, func(b *igmpResource, c igmpClient, sw types.String) { b.client, b.sw = c, sw })
}

type igmpModel struct {
	Switch            types.String `tfsdk:"switch"`
	ID                types.String `tfsdk:"id"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	ReportSuppression types.Bool   `tfsdk:"report_message_suppression"`
}

func (r *igmpResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_igmp_snooping"
}

func (r *igmpResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "IGMP snooping (Switching > IGMP Snooping). Singleton; import with id `igmp_snooping`. " +
			"Destroying only removes it from state. With snooping off, multicast is flooded to all ports.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Always `igmp_snooping`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled": schema.BoolAttribute{Required: true, Description: "Whether IGMP snooping is enabled."},
			"report_message_suppression": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Whether IGMP report message suppression is enabled. Defaults to false."},
		},
	}
}

func (r *igmpResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// igmpToModel converts the switch state to the Terraform model. The learned multicast groups are
// not part of the resource: they change at runtime (see the igmp_snooping data source).
func igmpToModel(sw types.String, s model.IGMP) igmpModel {
	return igmpModel{ID: qualifiedID(sw, igmpID), Enabled: types.BoolValue(s.Enabled), ReportSuppression: types.BoolValue(s.ReportSuppression)}
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *igmpResource) apply(ctx context.Context, plan *igmpModel, add func(string, string)) bool {
	b := r.bound(add, plan.Switch)
	return b != nil && b.applyBound(ctx, plan, add)
}

func (r *igmpResource) applyBound(ctx context.Context, plan *igmpModel, add func(string, string)) bool {
	want := model.IGMP{Enabled: plan.Enabled.ValueBool(), ReportSuppression: plan.ReportSuppression.ValueBool()}
	if err := r.client.SetIGMP(ctx, model.IGMPConfig{Enabled: want.Enabled, ReportSuppression: want.ReportSuppression}); err != nil {
		add("Setting IGMP snooping failed", err.Error())
		return false
	}
	got, err := r.client.IGMP(ctx)
	if err != nil {
		add("Reading IGMP snooping back failed", err.Error())
		return false
	}
	if got.Enabled != want.Enabled || got.ReportSuppression != want.ReportSuppression {
		add("IGMP snooping did not change", fmt.Sprintf("requested enabled=%t suppression=%t, switch reports enabled=%t suppression=%t",
			want.Enabled, want.ReportSuppression, got.Enabled, got.ReportSuppression))
		return false
	}
	*plan = igmpToModel(plan.Switch, got)
	return true
}

func (r *igmpResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *igmpResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *igmpResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *igmpResource) readBound(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	got, err := r.client.IGMP(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading IGMP snooping failed", err.Error())
		return
	}
	m := igmpToModel(r.sw, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *igmpResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *igmpResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingletonID(ctx, req, resp, igmpID)
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *igmpResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &igmpResource{}
