package provider

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

const managementIPID = "management_ip"

var (
	_ resource.Resource                   = &managementIPResource{}
	_ resource.ResourceWithConfigure      = &managementIPResource{}
	_ resource.ResourceWithImportState    = &managementIPResource{}
	_ resource.ResourceWithValidateConfig = &managementIPResource{}
	_ resource.ResourceWithModifyPlan     = &managementIPResource{}
)

// NewManagementIPResource returns the tplinkeasysmart_management_ip resource.
func NewManagementIPResource() resource.Resource { return &managementIPResource{} }

type managementIPResource struct {
	src    switchSource
	client managementIPClient // set on the per-call copy made by bound()
	sw     types.String       // the switch that copy is bound to, for ids
}

// managementIPClient is the part of the switch API this resource needs.
type managementIPClient interface {
	Info(ctx context.Context) (escp.Info, error)
	SetIPConfig(ctx context.Context, cfg escp.IPConfig) error
	EnableDHCP(ctx context.Context) error
}

// bound returns a copy of the resource wired to the client of the named switch.
func (r *managementIPResource) bound(add func(string, string), name types.String) *managementIPResource {
	return bindSwitch(add, r.src, name, r, func(b *managementIPResource, c managementIPClient, sw types.String) { b.client, b.sw = c, sw })
}

type managementIPModel struct {
	Switch          types.String `tfsdk:"switch"`
	ID              types.String `tfsdk:"id"`
	IPAddress       types.String `tfsdk:"ip_address"`
	Netmask         types.String `tfsdk:"netmask"`
	Gateway         types.String `tfsdk:"gateway"`
	DHCP            types.Bool   `tfsdk:"dhcp"`
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *managementIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_management_ip"
}

func (r *managementIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The management address of the switch (System > IP Setting): a static IP address, subnet mask and default gateway, or a lease from a DHCP server (`dhcp = true`). Singleton; import with id `management_ip`. " +
			"**Safety:** changing it moves the switch, which can cut the management path, so the provider refuses unless `allow_disruptive = true`. Changing a static address needs the new address in the provider's `host`, " +
			"or the switch's MAC in the provider's `mac`, which finds it wherever it is. Turning DHCP on needs `mac` as well, because the leased address is not known in advance. " +
			"The device description is sent back as the switch reports it. Applying what the switch already has writes nothing. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Always `management_ip`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"ip_address": schema.StringAttribute{Optional: true, Computed: true, Description: "IPv4 address of the switch. Required with `dhcp = false`; must be a host address of the subnet and outside the DHCP pool of the router. " +
				"Must not be set with `dhcp = true`, where it reports the leased address."},
			"netmask": schema.StringAttribute{Optional: true, Computed: true, Description: "Subnet mask, a contiguous prefix between /8 (`255.0.0.0`) and /30 (`255.255.255.252`). Required with `dhcp = false`; reports the leased mask with `dhcp = true`."},
			"gateway": schema.StringAttribute{Optional: true, Computed: true, Description: "Default gateway: another host address of the subnet, or `0.0.0.0` for none. A wrong gateway only cuts the switch's route out of its subnet. " +
				"Required with `dhcp = false`; reports the leased gateway with `dhcp = true`."},
			"dhcp": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false),
				Description: "Lease the address from a DHCP server instead of using a static one. Defaults to false. Turning it on needs the switch's `mac` in the provider configuration, " +
					"because the switch takes the address the server gives it, which only a lookup by MAC can find again. A switch that leases an address ignores a static one, so turning it off needs `ip_address`, `netmask` and `gateway`."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow moving the switch to another address, mask or gateway, or between a static address and a lease. Defaults to false."),
		},
	}
}

func (r *managementIPResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// ipConfigOf parses the three address attributes; ok is false (with the
// problem named on the attribute) when one is not an IPv4 address.
func ipConfigOf(m managementIPModel, add func(path.Path, string, string)) (escp.IPConfig, bool) {
	ok := true
	parse := func(attr string, v types.String) netip.Addr {
		ip, err := escp.ParseIPv4(v.ValueString())
		if err != nil {
			add(path.Root(attr), "Not an IPv4 address", fmt.Sprintf("%q is not a dotted IPv4 address", v.ValueString()))
			ok = false
		}
		return ip
	}
	cfg := escp.IPConfig{IP: parse("ip_address", m.IPAddress), Netmask: parse("netmask", m.Netmask), Gateway: parse("gateway", m.Gateway)}
	return cfg, ok
}

// ValidateConfig refuses, at plan time, an address the switch cannot use, a static address next to
// `dhcp = true` (the switch would ignore it) and a missing one next to `dhcp = false`.
func (r *managementIPResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m managementIPModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if m.DHCP.IsUnknown() {
		return
	}
	addrs := []struct {
		attr  string
		value types.String
	}{{"ip_address", m.IPAddress}, {"netmask", m.Netmask}, {"gateway", m.Gateway}}
	if m.DHCP.ValueBool() {
		for _, a := range addrs {
			if !a.value.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root(a.attr), "A static address next to DHCP",
					"A switch that leases its address ignores a static one, so `"+a.attr+"` must not be set with `dhcp = true`; the attribute reports the leased value.")
			}
		}
		return
	}
	for _, a := range addrs {
		if a.value.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root(a.attr), "Missing static address", "`"+a.attr+"` is required unless `dhcp = true`.")
		}
	}
	if resp.Diagnostics.HasError() || m.IPAddress.IsUnknown() || m.Netmask.IsUnknown() || m.Gateway.IsUnknown() {
		return
	}
	cfg, ok := ipConfigOf(m, resp.Diagnostics.AddAttributeError)
	if !ok {
		return
	}
	if err := escp.ValidateIPConfig(cfg); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("ip_address"), "The management address cannot be used", err.Error())
	}
}

// apply binds the resource to the switch the plan selects, then applies it.
func (r *managementIPResource) apply(ctx context.Context, m *managementIPModel, add func(string, string)) bool {
	b := r.bound(add, m.Switch)
	return b != nil && b.applyBound(ctx, m, add)
}

func (r *managementIPResource) applyBound(ctx context.Context, m *managementIPModel, add func(string, string)) bool {
	cur, err := r.client.Info(ctx)
	if err != nil {
		add("Reading the switch failed", err.Error())
		return false
	}
	wantDHCP := m.DHCP.ValueBool()
	var want escp.IPConfig
	if !wantDHCP {
		var ok bool
		if want, ok = ipConfigOf(*m, func(_ path.Path, summary, detail string) { add(summary, detail) }); !ok {
			return false // plan-time validation refuses this already; nothing is compared or written with half an address
		}
	}
	if wantDHCP != cur.DHCP || (!wantDHCP && !sameIPConfig(cur, want)) {
		if !m.AllowDisruptive.ValueBool() {
			add("Refusing to change the management address", fmt.Sprintf("the switch is at ip %s mask %s gateway %s with DHCP %s and %s was requested. A wrong address, mask "+
				"or gateway can cut the management path for good (recovery is a physical factory reset); set allow_disruptive = true to proceed", cur.IP, cur.Netmask, cur.Gateway, onOff(cur.DHCP), requested(wantDHCP, want)))
			return false
		}
		if !r.write(ctx, wantDHCP, want, add) {
			return false
		}
		got, err := r.client.Info(ctx)
		if err != nil || got.DHCP != wantDHCP || (!wantDHCP && !sameIPConfig(got, want)) {
			add("The management address did not change as requested", fmt.Sprintf("requested %s, the switch reports ip %s mask %s gateway %s with DHCP %s (err=%v)",
				requested(wantDHCP, want), got.IP, got.Netmask, got.Gateway, onOff(got.DHCP), err))
			return false
		}
		cur = got
		tflog.Info(ctx, "management address changed; the provider's host must point at the new address from now on, or its mac must be set", map[string]any{"ip": got.IP, "dhcp": got.DHCP})
	}
	if wantDHCP { // the leased values are what the switch reports
		m.IPAddress, m.Netmask, m.Gateway = types.StringValue(addrString(cur.IP)), types.StringValue(addrString(cur.Netmask)), types.StringValue(addrString(cur.Gateway))
	}
	m.ID = qualifiedID(r.sw, managementIPID)
	return true
}

// write sends the change: a static address (which also turns DHCP off) or DHCP on.
func (r *managementIPResource) write(ctx context.Context, dhcp bool, static escp.IPConfig, add func(string, string)) bool {
	if !dhcp {
		if err := r.client.SetIPConfig(ctx, static); err != nil {
			add("Setting the management address failed", fmt.Sprintf("%v. The switch may now be at the old or the new address; look at both before applying again", err))
			return false
		}
		return true
	}
	if err := r.client.EnableDHCP(ctx); err != nil {
		hint := "The switch may now be at its old address or at a leased one; look for it by its MAC address before applying again."
		if errors.Is(err, escp.ErrInvalid) {
			hint = "Set `mac` in the provider configuration (or the entry of `switches`) so the switch can be found at the address the DHCP server gives it."
		}
		add("Turning DHCP on failed", fmt.Sprintf("%v. %s", err, hint))
		return false
	}
	return true
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// requested says what a plan asked for, for messages.
func requested(dhcp bool, static escp.IPConfig) string {
	if dhcp {
		return "a DHCP lease"
	}
	return static.String()
}

// sameIPConfig compares the address triple of a discovery reply with the wanted one.
func sameIPConfig(info escp.Info, want escp.IPConfig) bool {
	return info.IP == want.IP && info.Netmask == want.Netmask && info.Gateway == want.Gateway
}

func (r *managementIPResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

func (r *managementIPResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, r.apply)
}

// Read binds the resource to the switch recorded in state, then reads it.
func (r *managementIPResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	sw := switchOf(ctx, req.State, &resp.Diagnostics)
	if b := r.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (r *managementIPResource) readBound(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	info, err := r.client.Info(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading the switch failed", err.Error())
		return
	}
	// resp.State starts as a copy of the prior state, so allow_disruptive and the switch are kept.
	for attr, v := range map[string]string{"id": qualifiedID(r.sw, managementIPID).ValueString(), "ip_address": addrString(info.IP), "netmask": addrString(info.Netmask), "gateway": addrString(info.Gateway)} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attr), v)...)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("dhcp"), info.DHCP)...)
}

// Delete only forgets the resource: the switch must keep some address.
func (r *managementIPResource) Delete(ctx context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	tflog.Debug(ctx, "tplinkeasysmart_management_ip removed from state; the management address on the switch is left unchanged")
}

func (r *managementIPResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if importSingletonID(ctx, req, resp, managementIPID) {
		resetAllowDisruptive(ctx, resp)
	}
}

// ModifyPlan refuses, while planning, a switch that cannot be resolved, and says what a lease will
// report: nothing is known about it before the apply, unless the switch already leases an address
// (then the state's values stand, and a renewal that changes them shows up in the refresh).
func (r *managementIPResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan managementIPModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.DHCP.IsUnknown() || !plan.DHCP.ValueBool() {
		return
	}
	leased := types.StringUnknown()
	values := [3]types.String{leased, leased, leased}
	if !req.State.Raw.IsNull() {
		var state managementIPModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if state.DHCP.ValueBool() {
			values = [3]types.String{state.IPAddress, state.Netmask, state.Gateway}
		}
	}
	for i, attr := range []string{"ip_address", "netmask", "gateway"} {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(attr), values[i])...)
	}
}
