package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &lagResource{}
	_ resource.ResourceWithConfigure   = &lagResource{}
	_ resource.ResourceWithImportState = &lagResource{}
)

// NewLAGResource returns the tplinkeasysmart_lag resource.
func NewLAGResource() resource.Resource { return &lagResource{} }

type lagResource struct{ client SwitchClient }

type lagResourceModel struct {
	Group           types.Int64 `tfsdk:"group"`
	Ports           types.Set   `tfsdk:"ports"`
	AllowDisruptive types.Bool  `tfsdk:"allow_disruptive"`
}

func (r *lagResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lag"
}

func (r *lagResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A link aggregation group (Switching > Port Trunk). Import with the group number. **Safety:** the provider refuses to create, change or " +
			"destroy a group when any port involved (old or new members) has a link up or belongs to another LAG, because that can cut the uplink; set " +
			"`allow_disruptive = true` to override. The existing router uplink (usually LAG1) should be imported and protected with `lifecycle { prevent_destroy = true }`. " +
			"The switch itself rejects ports that conflict with mirroring, VLANs, rate limits or storm control.",
		Attributes: map[string]schema.Attribute{
			"group": schema.Int64Attribute{Required: true, Description: "Group number (1-based).",
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"ports": schema.SetAttribute{Required: true, ElementType: types.Int64Type, Description: "Member ports."},
			"allow_disruptive": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Allow touching ports that have a link up or belong to a LAG. Defaults to false."},
		},
	}
}

func (r *lagResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// guardLAG checks every port of the old and new membership. Ports that are
// already members of this very group count as in a LAG, so they are only
// accepted when allow_disruptive is set or they are about to be added.
func (r *lagResource) guardLAG(ctx context.Context, group int, allow bool, old, new []int) error {
	if allow {
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

func (r *lagResource) apply(ctx context.Context, m *lagResourceModel, add func(string, string)) bool {
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
	var plan lagResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *lagResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan lagResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && r.apply(ctx, &plan, resp.Diagnostics.AddError) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
}

func (r *lagResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
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
func (r *lagResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
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

func (r *lagResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := strconv.Atoi(req.ID)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError("Invalid import id", fmt.Sprintf("expected a LAG group number, got %q", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group"), int64(n))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("allow_disruptive"), false)...)
}
