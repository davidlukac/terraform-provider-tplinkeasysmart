package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

const igmpID = "igmp_snooping"

var (
	_ resource.Resource                = &igmpResource{}
	_ resource.ResourceWithConfigure   = &igmpResource{}
	_ resource.ResourceWithImportState = &igmpResource{}
)

// NewIGMPSnoopingResource returns the tplinkeasysmart_igmp_snooping resource.
func NewIGMPSnoopingResource() resource.Resource { return &igmpResource{} }

type igmpResource struct{ client SwitchClient }

type igmpModel struct {
	ID                types.String `tfsdk:"id"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	ReportSuppression types.Bool   `tfsdk:"report_message_suppression"`
	Groups            types.List   `tfsdk:"multicast_groups"`
}

func (r *igmpResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_igmp_snooping"
}

func (r *igmpResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "IGMP snooping (Switching > IGMP Snooping). Singleton; import with id `igmp_snooping`. " +
			"Destroying only removes it from state. With snooping off, multicast is flooded to all ports.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `igmp_snooping`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled": schema.BoolAttribute{Required: true, Description: "Whether IGMP snooping is enabled."},
			"report_message_suppression": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Whether IGMP report message suppression is enabled. Defaults to false."},
			"multicast_groups": schema.ListNestedAttribute{Computed: true, Description: "Multicast groups currently learned (read-only, changes at runtime).",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"ip":    schema.StringAttribute{Computed: true, Description: "Multicast group address."},
					"vlan":  schema.Int64Attribute{Computed: true, Description: "VLAN the group was learned in."},
					"ports": schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Ports with subscribers."},
				}}},
		},
	}
}

func (r *igmpResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

var igmpGroupType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"ip": types.StringType, "vlan": types.Int64Type, "ports": types.ListType{ElemType: types.Int64Type},
}}

// igmpToModel converts the switch state to the Terraform model. The values are
// built from the group type itself, so they cannot violate it.
func igmpToModel(s escp.IGMP) igmpModel {
	groups := make([]attr.Value, 0, len(s.Groups))
	for _, g := range s.Groups {
		ports := make([]attr.Value, 0, len(g.Ports))
		for _, p := range g.Ports {
			ports = append(ports, types.Int64Value(int64(p)))
		}
		groups = append(groups, types.ObjectValueMust(igmpGroupType.AttrTypes, map[string]attr.Value{
			"ip":    types.StringValue(g.IP),
			"vlan":  types.Int64Value(int64(g.VLAN)),
			"ports": types.ListValueMust(types.Int64Type, ports),
		}))
	}
	return igmpModel{ID: types.StringValue(igmpID), Enabled: types.BoolValue(s.Enabled),
		ReportSuppression: types.BoolValue(s.ReportSuppression), Groups: types.ListValueMust(igmpGroupType, groups)}
}

func (r *igmpResource) write(ctx context.Context, plan igmpModel, diags *diag.Diagnostics) (igmpModel, bool) {
	want := escp.IGMP{Enabled: plan.Enabled.ValueBool(), ReportSuppression: plan.ReportSuppression.ValueBool()}
	if err := r.client.SetIGMP(ctx, want.Enabled, want.ReportSuppression); err != nil {
		diags.AddError("Setting IGMP snooping failed", err.Error())
		return igmpModel{}, false
	}
	got, err := r.client.IGMP(ctx)
	if err != nil {
		diags.AddError("Reading IGMP snooping back failed", err.Error())
		return igmpModel{}, false
	}
	if got.Enabled != want.Enabled || got.ReportSuppression != want.ReportSuppression {
		diags.AddError("IGMP snooping did not change", fmt.Sprintf("requested enabled=%t suppression=%t, switch reports enabled=%t suppression=%t",
			want.Enabled, want.ReportSuppression, got.Enabled, got.ReportSuppression))
		return igmpModel{}, false
	}
	return igmpToModel(got), true
}

func (r *igmpResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan igmpModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m, ok := r.write(ctx, plan, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *igmpResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan igmpModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if m, ok := r.write(ctx, plan, &resp.Diagnostics); ok {
		resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
	}
}

func (r *igmpResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	got, err := r.client.IGMP(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading IGMP snooping failed", err.Error())
		return
	}
	m := igmpToModel(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *igmpResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *igmpResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != igmpID {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("the only valid id is %q, got %q", igmpID, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), igmpID)...)
}
