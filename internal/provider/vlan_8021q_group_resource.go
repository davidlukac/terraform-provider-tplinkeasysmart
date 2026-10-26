package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

var (
	_ resource.Resource                = &vlan8021QGroupResource{}
	_ resource.ResourceWithConfigure   = &vlan8021QGroupResource{}
	_ resource.ResourceWithImportState = &vlan8021QGroupResource{}
)

// NewVLAN8021QGroupResource returns the tplinkeasysmart_vlan_8021q_group resource.
func NewVLAN8021QGroupResource() resource.Resource { return &vlan8021QGroupResource{} }

type vlan8021QGroupResource struct {
	src    switchSource
	client vlan8021QGroupClient // set on the per-call copy made by bound()
	sw     types.String         // the switch that copy is bound to, for ids
}

// vlan8021QGroupClient is the part of the switch API this resource needs.
type vlan8021QGroupClient interface {
	Dot1Q(ctx context.Context) (escp.Dot1Q, error)
	SetVLAN8021Q(ctx context.Context, v escp.VLAN8021Q) error
	DeleteVLAN8021Q(ctx context.Context, id int) error
	Ports(ctx context.Context) ([]escp.Port, error)
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *vlan8021QGroupResource) bound(add func(string, string), name types.String) *vlan8021QGroupResource {
	return bindSwitch(add, r.src, name, r, func(b *vlan8021QGroupResource, c vlan8021QGroupClient, sw types.String) { b.client, b.sw = c, sw })
}

type vlan8021QGroupModel struct {
	Switch          types.String `tfsdk:"switch"`
	VLAN            types.Int64  `tfsdk:"vlan"`
	Name            types.String `tfsdk:"name"`
	TaggedPorts     types.Set    `tfsdk:"tagged_ports"`
	UntaggedPorts   types.Set    `tfsdk:"untagged_ports"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *vlan8021QGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_8021q_group"
}

func (r *vlan8021QGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	portSet := func(description string) schema.SetAttribute {
		return schema.SetAttribute{Optional: true, Computed: true, ElementType: types.Int64Type, Default: setdefault.StaticValue(emptyPortSet()),
			Validators: []validator.Set{validPortSet()}, Description: description}
	}
	resp.Schema = schema.Schema{
		Description: "An 802.1Q VLAN (VLAN > 802.1Q VLAN): its name and its tagged and untagged member ports. Import with the VLAN id. " +
			"The 802.1Q mode must already be active (see `tplinkeasysmart_vlan_mode`); the provider refuses to manage VLANs in another mode. " +
			"Unlike port-based groups, a port can be a member of several 802.1Q VLANs. **Safety:** the provider refuses to create, change or destroy a VLAN " +
			"when any port involved (old or new members) has a link up or belongs to a LAG, and when the change would leave a port in no VLAN at all, because " +
			"that can cut connectivity or management access; set `allow_disruptive = true` to override. Keep VLAN 1 (the default) under `lifecycle { prevent_destroy = true }`.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"vlan": schema.Int64Attribute{Required: true, Description: "VLAN id, " + vlanIDRangeText() + ".",
				Validators:    []validator.Int64{validVLANID()},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"name": schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString(""),
				Validators: validVLANName(), Description: "VLAN name, up to 10 letters, digits, hyphens or underscores. Defaults to empty."},
			"tagged_ports":      portSet("Ports that carry this VLAN tagged. Defaults to none."),
			"untagged_ports":    portSet("Ports that carry this VLAN untagged. Defaults to none."),
			attrAllowDisruptive: allowDisruptiveAttribute("Allow touching ports that have a link up or belong to a LAG, and leaving a port in no VLAN. Defaults to false."),
		},
	}
}

func (r *vlan8021QGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// table reads the 802.1Q table; the mode must be active, because the switch ignores or rejects VLAN writes otherwise.
func (r *vlan8021QGroupResource) table(ctx context.Context) ([]escp.VLAN8021Q, error) {
	q, err := r.client.Dot1Q(ctx)
	if err != nil {
		return nil, err
	}
	if !q.Enabled {
		return nil, &inactiveModeError{"802.1Q VLAN mode is not active on the switch; refusing to manage 802.1Q VLANs in another VLAN mode (see tplinkeasysmart_vlan_mode)"}
	}
	return q.VLANs, nil
}

// findVLAN returns the VLAN with the id, if the table has it.
func findVLAN(table []escp.VLAN8021Q, id int) (escp.VLAN8021Q, bool) {
	for _, v := range table {
		if v.ID == id {
			return v, true
		}
	}
	return escp.VLAN8021Q{}, false
}

// members lists every member port of v, tagged and untagged, sorted.
func members(v escp.VLAN8021Q) []int {
	all := append(append([]int{}, v.Tagged...), v.Untagged...)
	sort.Ints(all)
	return all
}

// orphanedPorts returns the ports of affected that would belong to no VLAN
// once VLAN id has the given member ports (none: it is deleted); the other VLANs of the table stay as they are.
func orphanedPorts(table []escp.VLAN8021Q, id int, newMembers, affected []int) []int {
	covered := map[int]bool{}
	for _, v := range table {
		if v.ID == id {
			continue
		}
		for _, p := range members(v) {
			covered[p] = true
		}
	}
	for _, p := range newMembers {
		covered[p] = true
	}
	orphans := []int{}
	for _, p := range affected {
		if !covered[p] {
			orphans = append(orphans, p)
		}
	}
	sort.Ints(orphans)
	return orphans
}

// guard refuses a change of VLAN id from the old to the new members when a port
// involved carries traffic, or would be left in no VLAN.
func (r *vlan8021QGroupResource) guard(ctx context.Context, allow bool, table []escp.VLAN8021Q, id int, old, new []int) error {
	affected := append(append([]int{}, old...), new...)
	if err := guardPorts(ctx, r.client, allow, affected...); err != nil {
		return err
	}
	if allow {
		return nil
	}
	if orphans := orphanedPorts(table, id, new, affected); len(orphans) > 0 {
		return fmt.Errorf("the change would leave port(s) %v in no VLAN, so they could not carry any traffic; set allow_disruptive = true to proceed anyway", orphans)
	}
	return nil
}

func (m *vlan8021QGroupModel) vlan(ctx context.Context) (escp.VLAN8021Q, error) {
	tagged, err := setToInts(ctx, m.TaggedPorts)
	if err != nil {
		return escp.VLAN8021Q{}, err
	}
	untagged, err := setToInts(ctx, m.UntaggedPorts)
	if err != nil {
		return escp.VLAN8021Q{}, err
	}
	return escp.VLAN8021Q{ID: int(m.VLAN.ValueInt64()), Name: m.Name.ValueString(), Tagged: tagged, Untagged: untagged}, nil
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *vlan8021QGroupResource) apply(ctx context.Context, m *vlan8021QGroupModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *vlan8021QGroupResource) applyBound(ctx context.Context, m *vlan8021QGroupModel, add func(string, string)) bool {
	want, err := m.vlan(ctx)
	if err != nil {
		add("Invalid ports", err.Error())
		return false
	}
	table, err := r.table(ctx)
	if err != nil {
		add("Reading the 802.1Q VLANs failed", err.Error())
		return false
	}
	old, _ := findVLAN(table, want.ID)
	if err := r.guard(ctx, m.AllowDisruptive.ValueBool(), table, want.ID, members(old), members(want)); err != nil {
		add("Refusing to change the 802.1Q VLAN", err.Error())
		return false
	}
	if err := r.client.SetVLAN8021Q(ctx, want); err != nil {
		add("Setting the 802.1Q VLAN failed", err.Error())
		return false
	}
	after, err := r.table(ctx)
	got, ok := findVLAN(after, want.ID)
	if err != nil || !ok || !sameVLAN(got, want) {
		add("The 802.1Q VLAN did not change as requested", fmt.Sprintf("requested %+v, the switch reports %+v (found=%t, err=%v)", want, got, ok, err))
		return false
	}
	return true
}

// sameVLAN compares two VLANs by id, name and the sets of tagged and untagged ports.
func sameVLAN(a, b escp.VLAN8021Q) bool {
	sorted := func(p []int) string { c := append([]int{}, p...); sort.Ints(c); return fmt.Sprint(c) }
	return a.ID == b.ID && a.Name == b.Name && sorted(a.Tagged) == sorted(b.Tagged) && sorted(a.Untagged) == sorted(b.Untagged)
}

func (r *vlan8021QGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *vlan8021QGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyUpdate(ctx, req, resp, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *vlan8021QGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *vlan8021QGroupResource) readBound(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vlan8021QGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	table, err := r.table(ctx)
	if err != nil {
		if !warnInactiveMode(&resp.Diagnostics, err, "the 802.1Q VLANs", "The state of this resource is kept as it is and cannot be checked until the mode is switched back; changes are refused meanwhile") {
			resp.Diagnostics.AddError("Reading the 802.1Q VLANs failed", err.Error())
		}
		return
	}
	got, ok := findVLAN(table, int(state.VLAN.ValueInt64()))
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}
	state.Name, state.TaggedPorts, state.UntaggedPorts = types.StringValue(got.Name), intsToSet(got.Tagged), intsToSet(got.Untagged)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// deleteBound removes the VLAN, under the same guard as every other change.
func (r *vlan8021QGroupResource) deleteBound(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vlan8021QGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := int(state.VLAN.ValueInt64())
	table, err := r.table(ctx)
	if err != nil {
		if !warnInactiveMode(&resp.Diagnostics, err, "the 802.1Q VLANs", "The VLAN cannot be removed meanwhile, so the resource is only forgotten and the switch is left as it is") {
			resp.Diagnostics.AddError("Reading the 802.1Q VLANs failed", err.Error())
		}
		return
	}
	old, found := findVLAN(table, id)
	if !found {
		return // already gone
	}
	if err := r.guard(ctx, state.AllowDisruptive.ValueBool(), table, id, members(old), nil); err != nil {
		resp.Diagnostics.AddError("Refusing to remove the 802.1Q VLAN", err.Error())
		return
	}
	if err := r.client.DeleteVLAN8021Q(ctx, id); err != nil {
		resp.Diagnostics.AddError("Removing the 802.1Q VLAN failed", err.Error())
		return
	}
	after, err := r.table(ctx)
	if _, still := findVLAN(after, id); err != nil || still {
		resp.Diagnostics.AddError("The 802.1Q VLAN was not removed", fmt.Sprintf("VLAN %d is still on the switch (err=%v)", id, err))
	}
}

// Delete binds the resource to the switch recorded in state, then removes it.
func (r *vlan8021QGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if b := r.bound(resp.Diagnostics.AddError, switchOf(ctx, req.State, &resp.Diagnostics)); b != nil {
		b.deleteBound(ctx, req, resp)
	}
}

func (r *vlan8021QGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importPositiveInt(ctx, req, resp, "a VLAN id", "vlan") {
		resetAllowDisruptive(ctx, resp)
	}
}

// ValidateConfig rejects, at plan time, a port that is both tagged and untagged and a VLAN without ports.
func (r *vlan8021QGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m vlan8021QGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	tagged, okT := portsOf(ctx, m.TaggedPorts)
	untagged, okU := portsOf(ctx, m.UntaggedPorts)
	if !okT || !okU {
		return
	}
	if len(tagged)+len(untagged) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("untagged_ports"), "VLAN without ports", "an 802.1Q VLAN needs at least one tagged or untagged port")
	}
	seen := map[int]bool{}
	for _, p := range untagged {
		seen[p] = true
	}
	for _, p := range tagged {
		if seen[p] {
			resp.Diagnostics.AddAttributeError(path.Root("tagged_ports"), "Port both tagged and untagged", fmt.Sprintf("port %d is in tagged_ports and untagged_ports", p))
		}
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *vlan8021QGroupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}

var (
	_ resource.ResourceWithValidateConfig = &vlan8021QGroupResource{}
	_ resource.ResourceWithModifyPlan     = &vlan8021QGroupResource{}
)
