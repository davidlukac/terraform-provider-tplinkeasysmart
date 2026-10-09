package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const loopPreventionID = "loop_prevention"

var (
	_ resource.Resource                = &loopPreventionResource{}
	_ resource.ResourceWithConfigure   = &loopPreventionResource{}
	_ resource.ResourceWithImportState = &loopPreventionResource{}
)

// NewLoopPreventionResource returns the tplinkeasysmart_loop_prevention resource.
func NewLoopPreventionResource() resource.Resource { return &loopPreventionResource{} }

type loopPreventionResource struct{ client SwitchClient }

type loopPreventionModel struct {
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func (r *loopPreventionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_loop_prevention"
}

func (r *loopPreventionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Loop prevention (Monitoring > Loop Prevention). Singleton; import with id `loop_prevention`. " +
			"Keep it enabled on a production network. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `loop_prevention`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled": schema.BoolAttribute{Required: true, Description: "Whether loop prevention is enabled."},
		},
	}
}

func (r *loopPreventionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *loopPreventionResource) apply(ctx context.Context, plan *loopPreventionModel, add func(string, string)) bool {
	want := plan.Enabled.ValueBool()
	if err := r.client.SetLoopPrevention(ctx, want); err != nil {
		add("Setting loop prevention failed", err.Error())
		return false
	}
	got, err := r.client.LoopPrevention(ctx)
	if err != nil {
		add("Reading loop prevention back failed", err.Error())
		return false
	}
	if got != want {
		add("Loop prevention did not change", fmt.Sprintf("requested enabled=%t but the switch reports enabled=%t", want, got))
		return false
	}
	plan.ID, plan.Enabled = types.StringValue(loopPreventionID), types.BoolValue(got)
	return true
}

func (r *loopPreventionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan loopPreventionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *loopPreventionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan loopPreventionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *loopPreventionResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	on, err := r.client.LoopPrevention(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading loop prevention failed", err.Error())
		return
	}
	m := loopPreventionModel{ID: types.StringValue(loopPreventionID), Enabled: types.BoolValue(on)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *loopPreventionResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *loopPreventionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != loopPreventionID {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("the only valid id is %q, got %q", loopPreventionID, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), loopPreventionID)...)
}
