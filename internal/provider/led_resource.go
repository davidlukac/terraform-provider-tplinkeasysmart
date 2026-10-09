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

const ledID = "led"

var (
	_ resource.Resource                = &ledResource{}
	_ resource.ResourceWithConfigure   = &ledResource{}
	_ resource.ResourceWithImportState = &ledResource{}
)

// NewLEDResource returns the tplinkeasysmart_led resource.
func NewLEDResource() resource.Resource { return &ledResource{} }

type ledResource struct{ client ledClient }

// ledClient is the part of the switch API this resource needs.
type ledClient interface {
	GetLED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
}

type ledModel struct {
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

func (r *ledResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_led"
}

func (r *ledResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The front-panel LEDs of the switch (System > LED On/Off in the web UI). " +
			"There is exactly one per switch; import it with the id `led`. Destroying the resource " +
			"only removes it from state and leaves the LEDs as they are.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "Always `led`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"enabled": schema.BoolAttribute{
				Required:    true,
				Description: "Whether the port LEDs are on.",
			},
		},
	}
}

func (r *ledResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom[ledClient](req.ProviderData, &resp.Diagnostics)
}

// apply writes the desired state and reads it back so the stored state is what
// the switch really reports.
func (r *ledResource) apply(ctx context.Context, plan *ledModel, add func(string, string)) bool {
	want := plan.Enabled.ValueBool()
	if err := r.client.SetLED(ctx, want); err != nil {
		add("Setting LED failed", err.Error())
		return false
	}
	got, err := r.client.GetLED(ctx)
	if err != nil {
		add("Reading LED back failed", err.Error())
		return false
	}
	if got != want {
		add("LED state did not change", fmt.Sprintf("requested enabled=%t but the switch reports enabled=%t", want, got))
		return false
	}
	plan.ID = types.StringValue(ledID)
	plan.Enabled = types.BoolValue(got)
	return true
}

func (r *ledResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *ledResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *ledResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ledModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	on, err := r.client.GetLED(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading LED failed", err.Error())
		return
	}
	state.ID = types.StringValue(ledID)
	state.Enabled = types.BoolValue(on)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete only forgets the resource: the LED has no "absent" state to restore.
func (r *ledResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *ledResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingletonID(ctx, req, resp, ledID)
}
