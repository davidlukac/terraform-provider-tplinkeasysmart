package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/escp-go"
)

const portMirrorID = "port_mirror"

var (
	_ resource.Resource                = &portMirrorResource{}
	_ resource.ResourceWithConfigure   = &portMirrorResource{}
	_ resource.ResourceWithImportState = &portMirrorResource{}
)

// NewPortMirrorResource returns the tplinkeasysmart_port_mirror resource.
func NewPortMirrorResource() resource.Resource { return &portMirrorResource{} }

type portMirrorResource struct {
	src    switchSource
	client portMirrorClient // set on the per-call copy made by bound()
	sw     types.String     // the switch that copy is bound to, for ids
}

// portMirrorClient is the part of the switch API this resource needs.
type portMirrorClient interface {
	PortMirror(ctx context.Context) (escp.Mirror, error)
	Ports(ctx context.Context) ([]escp.Port, error)
	SetPortMirror(ctx context.Context, m escp.Mirror) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *portMirrorResource) bound(add func(string, string), name types.String) *portMirrorResource {
	return bindSwitch(add, r.src, name, r, func(b *portMirrorResource, c portMirrorClient, sw types.String) { b.client, b.sw = c, sw })
}

type portMirrorResourceModel struct {
	Switch          types.String `tfsdk:"switch"`
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
			attrSwitch: resourceSwitchAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Always `port_mirror`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"enabled":     schema.BoolAttribute{Required: true, Description: "Whether mirroring is enabled. When false, no other port setting may be given."},
			"mirror_port": schema.Int64Attribute{Optional: true, Computed: true, Default: int64default.StaticInt64(0), Validators: []validator.Int64{int64validator.Between(0, escp.MaxPort)}, Description: "Destination (monitoring) port; 0 when disabled."},
			"ingress_ports": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Default: setdefault.StaticValue(emptyPortSet()), Validators: []validator.Set{validPortSet()},
				Description: "Ports whose received traffic is mirrored."},
			"egress_ports": schema.SetAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Default: setdefault.StaticValue(emptyPortSet()), Validators: []validator.Set{validPortSet()},
				Description: "Ports whose transmitted traffic is mirrored."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow a destination port that has a link up or is a LAG member. Defaults to false."),
		},
	}
}

func (r *portMirrorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *portMirrorResource) apply(ctx context.Context, m *portMirrorResourceModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *portMirrorResource) applyBound(ctx context.Context, m *portMirrorResourceModel, add func(string, string)) bool {
	in, err1 := setToPorts(ctx, m.IngressPorts)
	eg, err2 := setToPorts(ctx, m.EgressPorts)
	if err1 != nil || err2 != nil {
		add("Invalid port set", fmt.Sprintf("%v %v", err1, err2))
		return false
	}
	want := escp.Mirror{Enabled: m.Enabled.ValueBool(), Destination: escp.PortID(m.MirrorPort.ValueInt64()), IngressPorts: in, EgressPorts: eg}
	if want.Enabled {
		if err := guardPorts(ctx, r.client, m.AllowDisruptive.ValueBool(), want.Destination); err != nil {
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
	if got != want {
		add("Port mirror did not change as requested", fmt.Sprintf("requested %+v, switch reports %+v", want, got))
		return false
	}
	m.ID = qualifiedID(r.sw, portMirrorID)
	return true
}

func (r *portMirrorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *portMirrorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyUpdate(ctx, req, resp, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *portMirrorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *portMirrorResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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
	state.ID = qualifiedID(r.sw, portMirrorID)
	state.Enabled, state.MirrorPort = types.BoolValue(got.Enabled), types.Int64Value(int64(got.Destination))
	state.IngressPorts, state.EgressPorts = portsToSet(got.IngressPorts), portsToSet(got.EgressPorts)
	if state.AllowDisruptive.IsNull() {
		state.AllowDisruptive = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *portMirrorResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *portMirrorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importSingletonID(ctx, req, resp, portMirrorID) {
		resetAllowDisruptive(ctx, resp)
	}
}

// ValidateConfig rejects, at plan time, a mirror that cannot work.
func (r *portMirrorResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m portMirrorResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	ingress, okIn := portsOf(ctx, m.IngressPorts)
	egress, okEg := portsOf(ctx, m.EgressPorts)
	if !okIn || !okEg {
		return // not fully known yet; the client checks again at apply time
	}
	if problem := mirrorProblem(m.Enabled, defaultZero(m.MirrorPort), ingress, egress); problem != "" {
		resp.Diagnostics.AddAttributeError(path.Root("enabled"), "Invalid port mirror configuration", problem)
	}
}

var _ resource.ResourceWithValidateConfig = &portMirrorResource{}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *portMirrorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var _ resource.ResourceWithModifyPlan = &portMirrorResource{}
