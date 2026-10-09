package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

const portMirrorID = "port_mirror"

var (
	_ resource.Resource                = &portMirrorResource{}
	_ resource.ResourceWithConfigure   = &portMirrorResource{}
	_ resource.ResourceWithImportState = &portMirrorResource{}
)

// NewPortMirrorResource returns the tplinkeasysmart_port_mirror resource.
func NewPortMirrorResource() resource.Resource { return &portMirrorResource{} }

type portMirrorResource struct{ client SwitchClient }

type portMirrorResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	MirrorPort      types.Int64  `tfsdk:"mirror_port"`
	IngressPorts    types.Set    `tfsdk:"ingress_ports"`
	EgressPorts     types.Set    `tfsdk:"egress_ports"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *portMirrorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_mirror"
}

func emptyPortSet() types.Set { return types.SetValueMust(types.Int64Type, []attr.Value{}) }

func (r *portMirrorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Port mirroring (Monitoring > Port Mirror). Singleton; import with id `port_mirror`. **Safety:** the destination port stops carrying normal traffic, " +
			"so by default the provider refuses a destination that has a link up or is in a LAG; set `allow_disruptive = true` to override. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `port_mirror`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled":     schema.BoolAttribute{Required: true, Description: "Whether mirroring is enabled. When false, no other port setting may be given."},
			"mirror_port": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Description: "Destination (monitoring) port; 0 when disabled."},
			"ingress_ports": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Default: setdefault.StaticValue(emptyPortSet()),
				Description: "Ports whose received traffic is mirrored."},
			"egress_ports": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Default: setdefault.StaticValue(emptyPortSet()),
				Description: "Ports whose transmitted traffic is mirrored."},
			"allow_disruptive": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Allow a destination port that has a link up or is a LAG member. Defaults to false."},
		},
	}
}

func (r *portMirrorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func setToInts(ctx context.Context, s types.Set) ([]int, error) {
	var vals []int64
	if d := s.ElementsAs(ctx, &vals, false); d.HasError() {
		return nil, fmt.Errorf("%v", d)
	}
	out := make([]int, 0, len(vals))
	for _, v := range vals {
		out = append(out, int(v))
	}
	sort.Ints(out)
	return out, nil
}

func intsToSet(in []int) types.Set {
	vals := make([]attr.Value, 0, len(in))
	for _, v := range in {
		vals = append(vals, types.Int64Value(int64(v)))
	}
	return types.SetValueMust(types.Int64Type, vals)
}

func (r *portMirrorResource) apply(ctx context.Context, m *portMirrorResourceModel, add func(string, string)) bool {
	in, err1 := setToInts(ctx, m.IngressPorts)
	eg, err2 := setToInts(ctx, m.EgressPorts)
	if err1 != nil || err2 != nil {
		add("Invalid port set", fmt.Sprintf("%v %v", err1, err2))
		return false
	}
	want := escp.Mirror{Enabled: m.Enabled.ValueBool(), MirrorPort: int(m.MirrorPort.ValueInt64()), IngressPorts: in, EgressPorts: eg}
	if want.Enabled {
		if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), want.MirrorPort); err != nil {
			add("Refusing to use this destination port", err.Error())
			return false
		}
	}
	if err := r.client.SetPortMirror(ctx, want); err != nil {
		add("Setting port mirror failed", err.Error())
		return false
	}
	got, err := r.client.PortMirror(ctx)
	if err != nil {
		add("Reading port mirror back failed", err.Error())
		return false
	}
	gin, geg := append([]int(nil), got.IngressPorts...), append([]int(nil), got.EgressPorts...)
	sort.Ints(gin)
	sort.Ints(geg)
	if got.Enabled != want.Enabled || got.MirrorPort != want.MirrorPort || fmt.Sprint(gin) != fmt.Sprint(in) || fmt.Sprint(geg) != fmt.Sprint(eg) {
		add("Port mirror did not change as requested", fmt.Sprintf("requested %+v, switch reports %+v", want, got))
		return false
	}
	return true
}

func (r *portMirrorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan portMirrorResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		plan.ID = types.StringValue(portMirrorID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *portMirrorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan portMirrorResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		plan.ID = types.StringValue(portMirrorID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *portMirrorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state portMirrorResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.client.PortMirror(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading port mirror failed", err.Error())
		return
	}
	state.ID = types.StringValue(portMirrorID)
	state.Enabled, state.MirrorPort = types.BoolValue(got.Enabled), types.Int64Value(int64(got.MirrorPort))
	state.IngressPorts, state.EgressPorts = intsToSet(got.IngressPorts), intsToSet(got.EgressPorts)
	if state.AllowDisruptive.IsNull() {
		state.AllowDisruptive = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *portMirrorResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *portMirrorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != portMirrorID {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("the only valid id is %q, got %q", portMirrorID, req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), portMirrorID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_disruptive"), false)...)
}
