package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &lagResource{}
	_ resource.ResourceWithConfigure   = &lagResource{}
	_ resource.ResourceWithImportState = &lagResource{}
)

// lagRangeNote states the group rule once, for the description and messages.
const lagRangeNote = "LAG 1 takes ports 1-4 and LAG 2 takes ports 5-8, each with 2 to 4 members"

// NewLAGResource returns the tplinkeasysmart_lag resource.
func NewLAGResource() resource.Resource { return &lagResource{} }

type lagResource struct {
	src    switchSource
	client lagClient    // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// lagClient is the part of the switch API this resource needs.
type lagClient interface {
	LAGs(ctx context.Context) ([]escp.LAG, error)
	Ports(ctx context.Context) ([]escp.Port, error)
	SetLAG(ctx context.Context, group int, ports []int) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *lagResource) bound(add func(string, string), name types.String) *lagResource {
	return bindSwitch(add, r.src, name, r, func(b *lagResource, c lagClient, sw types.String) { b.client, b.sw = c, sw })
}

type lagResourceModel struct {
	Switch          types.String `tfsdk:"switch"`
	Group           types.Int64  `tfsdk:"group"`
	Ports           types.Set    `tfsdk:"ports"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *lagResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lag"
}

func (r *lagResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A link aggregation group (Switching > Port Trunk). Import with the group number. **Safety:** the provider refuses to create, change or " +
			"destroy a group when any port involved (old or new members) has a link up or belongs to another LAG, because that can cut the uplink; set " +
			"`allow_disruptive = true` to override. The existing router uplink (usually LAG1) should be imported and protected with `lifecycle { prevent_destroy = true }`. " +
			"The group number decides the ports: " + lagRangeNote + ". The switch itself rejects ports that conflict with the port mirror (source or destination), a port-based VLAN other than the default, " +
			"a bandwidth limit or storm control; the error then names the conflict.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"group": schema.Int64Attribute{Required: true, Description: "Group number: 1 (ports 1-4) or 2 (ports 5-8).",
				Validators:    []validator.Int64{int64validator.Between(1, escp.LAGGroupCount)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"ports":             schema.SetAttribute{Required: true, ElementType: types.Int64Type, Validators: []validator.Set{validPortSet(), setvalidator.SizeBetween(escp.LAGMinPorts, escp.LAGPortsPerGroup)}, Description: "Member ports: 2 to 4, all within the range of the group (group 1: ports 1-4, group 2: ports 5-8)."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow touching ports that have a link up or belong to a LAG. Defaults to false."),
		},
	}
}

func (r *lagResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// guardLAG checks every port of the old and new membership. Ports that are
// already members of this very group count as in a LAG, so they are only
// accepted when allow_disruptive is set or they are about to be added.
func (r *lagResource) guardLAG(ctx context.Context, group int, allow bool, old, new []int) error {
	if allow {
		tflog.Warn(ctx, "allow_disruptive is set: the port-in-use guard is skipped")
		return nil
	}
	all, err := r.client.Ports(ctx)
	if err != nil {
		return fmt.Errorf("cannot check whether the ports are in use: %w", err)
	}
	seen := map[int]bool{}
	for _, p := range append(append([]int{}, old...), new...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		found := false
		for _, info := range all {
			if info.ID != p {
				continue
			}
			found = true
			if info.ActualSpeed != "link-down" {
				return fmt.Errorf("port %d has a link up (%s); changing its LAG membership can cut traffic or management access. Set allow_disruptive = true to proceed anyway", p, info.ActualSpeed)
			}
			if info.LAG != 0 && info.LAG != group {
				return fmt.Errorf("port %d already belongs to LAG%d", p, info.LAG)
			}
		}
		if !found {
			return fmt.Errorf("port %d does not exist on this switch", p)
		}
	}
	return nil
}

func (r *lagResource) current(ctx context.Context, group int) ([]int, error) {
	lags, err := r.client.LAGs(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range lags {
		if l.ID == group {
			p := append([]int(nil), l.Ports...)
			sort.Ints(p)
			return p, nil
		}
	}
	return nil, fmt.Errorf("LAG%d does not exist on this switch", group)
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *lagResource) apply(ctx context.Context, m *lagResourceModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *lagResource) applyBound(ctx context.Context, m *lagResourceModel, add func(string, string)) bool {
	group := int(m.Group.ValueInt64())
	ports, err := setToInts(ctx, m.Ports)
	if err != nil || len(ports) == 0 {
		add("Invalid ports", "a LAG needs at least one member port")
		return false
	}
	old, err := r.current(ctx, group)
	if err != nil {
		add("Reading LAG failed", err.Error())
		return false
	}
	if err := r.guardLAG(ctx, group, m.AllowDisruptive.ValueBool(), old, ports); err != nil {
		add("Refusing to change LAG", err.Error())
		return false
	}
	if err := r.client.SetLAG(ctx, group, ports); err != nil {
		add("Setting LAG failed", err.Error())
		return false
	}
	got, err := r.current(ctx, group)
	if err != nil || fmt.Sprint(got) != fmt.Sprint(ports) {
		add("LAG did not change as requested", fmt.Sprintf("requested %v, switch reports %v (err=%v)", ports, got, err))
		return false
	}
	return true
}

func (r *lagResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *lagResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *lagResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *lagResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state lagResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.current(ctx, int(state.Group.ValueInt64()))
	if err != nil {
		resp.Diagnostics.AddError("Reading LAG failed", err.Error())
		return
	}
	if len(got) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Ports = intsToSet(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Delete removes the group, under the same guard as every other change.
func (r *lagResource) deleteBound(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state lagResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group := int(state.Group.ValueInt64())
	old, err := r.current(ctx, group)
	if err != nil {
		resp.Diagnostics.AddError("Reading LAG failed", err.Error())
		return
	}
	if err := r.guardLAG(ctx, group, state.AllowDisruptive.ValueBool(), old, nil); err != nil {
		resp.Diagnostics.AddError("Refusing to remove LAG", err.Error())
		return
	}
	if err := r.client.SetLAG(ctx, group, nil); err != nil {
		resp.Diagnostics.AddError("Removing LAG failed", err.Error())
	}
}

// Delete binds the resource to the switch recorded in state, then removes it.
func (r *lagResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if b := r.bound(resp.Diagnostics.AddError, switchOf(ctx, req.State, &resp.Diagnostics)); b != nil {
		b.deleteBound(ctx, req, resp)
	}
}

func (r *lagResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a LAG group number", "group") {
		resetAllowDisruptive(ctx, resp)
	}
}

// ValidateConfig rejects, at plan time, member ports outside the range of the
// group. The count and the group number are checked by the schema validators,
// so they are not repeated here.
func (r *lagResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m lagResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	ports, ok := portsOf(ctx, m.Ports)
	if m.Group.IsNull() || m.Group.IsUnknown() || !ok {
		return
	}
	group := int(m.Group.ValueInt64())
	if group < 1 || group > escp.LAGGroupCount || len(ports) < escp.LAGMinPorts || len(ports) > escp.LAGPortsPerGroup {
		return
	}
	if err := escp.ValidateLAG(group, ports); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ports"), "Ports do not fit the LAG group", strings.TrimPrefix(err.Error(), "escp: "))
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *lagResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var (
	_ resource.ResourceWithValidateConfig = &lagResource{}
	_ resource.ResourceWithModifyPlan     = &lagResource{}
)
