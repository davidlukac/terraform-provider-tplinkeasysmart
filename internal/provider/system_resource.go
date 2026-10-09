package provider

import (
	"context"
	"fmt"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const systemID = "system"

var (
	_ resource.Resource                = &systemResource{}
	_ resource.ResourceWithConfigure   = &systemResource{}
	_ resource.ResourceWithImportState = &systemResource{}
)

// NewSystemResource returns the tplinkeasysmart_system resource.
func NewSystemResource() resource.Resource { return &systemResource{} }

type systemResource struct{ client systemClient }

// systemClient is the part of the switch API this resource needs.
type systemClient interface {
	Info(ctx context.Context) (escp.Info, error)
	SetHostname(ctx context.Context, name string) error
}

type systemResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Hostname types.String `tfsdk:"hostname"`
}

func (r *systemResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system"
}

func (r *systemResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "System settings (System > System Info > Device Description). Singleton; import with id `system`. " +
			"Only the device description is managed. The management IP, DHCP and gateway are never changed: the request re-sends them as read from the switch. " +
			"A write is skipped when the description already matches. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `system`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"hostname": schema.StringAttribute{Required: true,
				Description: "Device description: 1-32 letters, digits and hyphens, not starting or ending with a hyphen (dots and underscores are rejected by the switch)."},
		},
	}
}

func (r *systemResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom[systemClient](req.ProviderData, &resp.Diagnostics)
}

func (r *systemResource) apply(ctx context.Context, m *systemResourceModel, add func(string, string)) bool {
	want := m.Hostname.ValueString()
	cur, err := r.client.Info(ctx)
	if err != nil {
		add("Reading the switch failed", err.Error())
		return false
	}
	if cur.Hostname != want {
		if err := r.client.SetHostname(ctx, want); err != nil {
			add("Setting the device description failed", err.Error())
			return false
		}
		got, err := r.client.Info(ctx)
		if err != nil {
			add("Reading the device description back failed", err.Error())
			return false
		}
		if got.Hostname != want {
			add("Device description did not change", fmt.Sprintf("requested %q, switch reports %q", want, got.Hostname))
			return false
		}
		if got.IP != cur.IP || got.Netmask != cur.Netmask || got.Gateway != cur.Gateway || got.DHCP != cur.DHCP {
			add("Management IP configuration changed unexpectedly", fmt.Sprintf("before %+v, after %+v", cur, got))
			return false
		}
	}
	m.ID = types.StringValue(systemID)
	return true
}

func (r *systemResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *systemResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *systemResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	info, err := r.client.Info(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading the switch failed", err.Error())
		return
	}
	m := systemResourceModel{ID: types.StringValue(systemID), Hostname: types.StringValue(info.Hostname)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// Delete only forgets the resource: the device description has no "absent" state to restore.
func (r *systemResource) Delete(ctx context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	tflog.Debug(ctx, "tplinkeasysmart_system removed from state; the device description on the switch is left unchanged")
}

func (r *systemResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importSingletonID(ctx, req, resp, systemID)
}
