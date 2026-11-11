package provider

import (
	"context"
	"fmt"

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

type loopPreventionResource struct {
	src    switchSource
	client loopPreventionClient // set on the per-call copy made by bound()
	sw     types.String         // the switch that copy is bound to, for ids
}

// loopPreventionClient is the part of the switch API this resource needs.
type loopPreventionClient interface {
	LoopPrevention(ctx context.Context) (bool, error)
	SetLoopPrevention(ctx context.Context, on bool) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *loopPreventionResource) bound(add func(string, string), name types.String) *loopPreventionResource {
	return bindSwitch(add, r.src, name, r, func(b *loopPreventionResource, c loopPreventionClient, sw types.String) { b.client, b.sw = c, sw })
}

type loopPreventionModel struct {
	Switch          types.String `tfsdk:"switch"`
	ID              types.String `tfsdk:"id"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *loopPreventionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_loop_prevention"
}

func (r *loopPreventionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Loop prevention (Monitoring > Loop Prevention). Singleton; import with id `loop_prevention`. " +
			"Keep it enabled on a production network. **Safety:** turning it off removes the protection against a loop that would take the network down, and turning it on can make the switch block a port of a redundant topology, " +
			"so the provider refuses to change the setting unless `allow_disruptive = true`; applying the value the switch already has writes nothing. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Always `loop_prevention`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled":           schema.BoolAttribute{Required: true, Description: "Whether loop prevention is enabled."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow changing loop prevention. Defaults to false."),
		},
	}
}

func (r *loopPreventionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *loopPreventionResource) apply(ctx context.Context, plan *loopPreventionModel, add func(string, string)) bool {
	b := r.bound(add, plan.Switch)
	return b != nil && b.applyBound(ctx, plan, add)
}

// applyBound writes loop prevention when the switch differs from the plan, refusing a change that was
// not opted in to, and reads it back.
//
// readwrite: the singleton owns the setting and the write is the absolute value from the plan, never a
// value derived from the read, so a read that is stale by the time of the write cannot lose another
// writer's change to a different field; the read only decides whether to write (and which refusal to
// word), and the read-back after the write checks what the switch kept.
func (r *loopPreventionResource) applyBound(ctx context.Context, plan *loopPreventionModel, add func(string, string)) bool {
	want := plan.Enabled.ValueBool()
	have, err := r.client.LoopPrevention(ctx)
	if err != nil {
		add("Reading loop prevention failed", err.Error())
		return false
	}
	if have != want {
		if !plan.AllowDisruptive.ValueBool() {
			add("Refusing to change loop prevention", fmt.Sprintf("loop prevention is %s on the switch and %s was requested. Turning it off removes the protection against a loop; "+
				"turning it on can make the switch block a port of a redundant topology. Set allow_disruptive = true to proceed", onOff(have), onOff(want)))
			return false
		}
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
	}
	plan.ID = qualifiedID(r.sw, loopPreventionID)
	return true
}

func (r *loopPreventionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *loopPreventionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyUpdate(ctx, req, resp, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *loopPreventionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *loopPreventionResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var m loopPreventionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	on, err := r.client.LoopPrevention(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading loop prevention failed", err.Error())
		return
	}
	m.ID, m.Enabled = qualifiedID(r.sw, loopPreventionID), types.BoolValue(on)
	if m.AllowDisruptive.IsNull() {
		m.AllowDisruptive = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *loopPreventionResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *loopPreventionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importSingletonID(ctx, req, resp, loopPreventionID) {
		resetAllowDisruptive(ctx, resp)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *loopPreventionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &loopPreventionResource{}
