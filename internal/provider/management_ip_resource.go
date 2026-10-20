package provider

import (
	"context"
	"fmt"
	"net"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	AllowDisruptive types.Bool   `tfsdk:"allow_disruptive"`
}

func (r *managementIPResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_management_ip"
}

func (r *managementIPResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The static management address of the switch (System > IP Setting): IP address, subnet mask and default gateway. Singleton; import with id `management_ip`. " +
			"**Safety:** changing it moves the switch, which can cut the management path, so the provider refuses unless `allow_disruptive = true`. The DHCP flag and the " +
			"device description are sent back as the switch reports them (the switch must already use a static address). After the apply, point the provider's `host` " +
			"at the new address. Applying the address the switch already has writes nothing. Destroying only removes it from state.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: resourceSwitchAttribute(),
			"id": schema.StringAttribute{Computed: true, Description: "Always `management_ip`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"ip_address":        schema.StringAttribute{Required: true, Description: "IPv4 address of the switch. Must be a host address of the subnet; keep it out of the DHCP pool of the router."},
			"netmask":           schema.StringAttribute{Required: true, Description: "Subnet mask, a contiguous prefix between /8 (`255.0.0.0`) and /30 (`255.255.255.252`)."},
			"gateway":           schema.StringAttribute{Required: true, Description: "Default gateway: another host address of the subnet, or `0.0.0.0` for none. A wrong gateway only cuts the switch's route out of its subnet."},
			attrAllowDisruptive: allowDisruptiveAttribute("Allow moving the switch to another address, mask or gateway. Defaults to false."),
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
	parse := func(attr string, v types.String) net.IP {
		ip := net.ParseIP(v.ValueString()).To4()
		if ip == nil {
			add(path.Root(attr), "Not an IPv4 address", fmt.Sprintf("%q is not a dotted IPv4 address", v.ValueString()))
			ok = false
		}
		return ip
	}
	cfg := escp.IPConfig{IP: parse("ip_address", m.IPAddress), Netmask: parse("netmask", m.Netmask), Gateway: parse("gateway", m.Gateway)}
	return cfg, ok
}

// ValidateConfig refuses, at plan time, an address the switch cannot use.
func (r *managementIPResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var m managementIPModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if m.IPAddress.IsUnknown() || m.Netmask.IsUnknown() || m.Gateway.IsUnknown() {
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
	want, _ := ipConfigOf(*m, func(_ path.Path, summary, detail string) { add(summary, detail) })
	cur, err := r.client.Info(ctx)
	if err != nil {
		add("Reading the switch failed", err.Error())
		return false
	}
	if cur.DHCP {
		add("The switch uses DHCP", "the management address is leased from a DHCP server; this resource only manages a static address, so turn DHCP off in the web UI first")
		return false
	}
	if !sameIPConfig(cur, want) {
		if !m.AllowDisruptive.ValueBool() {
			add("Refusing to change the management address", fmt.Sprintf("the switch is at ip %s mask %s gateway %s and %s was requested. A wrong address, mask "+
				"or gateway can cut the management path for good (recovery is a physical factory reset); set allow_disruptive = true to proceed", cur.IP, cur.Netmask, cur.Gateway, want))
			return false
		}
		if err := r.client.SetIPConfig(ctx, want); err != nil {
			add("Setting the management address failed", fmt.Sprintf("%v. The switch may now be at the old or the new address; look at both before applying again", err))
			return false
		}
		got, err := r.client.Info(ctx)
		if err != nil || !sameIPConfig(got, want) {
			add("The management address did not change as requested", fmt.Sprintf("requested %s, the switch reports ip %s mask %s gateway %s (err=%v)", want, got.IP, got.Netmask, got.Gateway, err))
			return false
		}
		tflog.Info(ctx, "management address changed; the provider's host must point at the new address from now on", map[string]any{"ip": got.IP})
	}
	m.ID = qualifiedID(r.sw, managementIPID)
	return true
}

// sameIPConfig compares the address triple of a discovery reply with the wanted one.
func sameIPConfig(info escp.Info, want escp.IPConfig) bool {
	return info.IP == want.IP.String() && info.Netmask == want.Netmask.String() && info.Gateway == want.Gateway.String()
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
	for attr, v := range map[string]string{"id": qualifiedID(r.sw, managementIPID).ValueString(), "ip_address": info.IP, "netmask": info.Netmask, "gateway": info.Gateway} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attr), v)...)
	}
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

// ModifyPlan refuses, while planning, a switch that cannot be resolved.
func (r *managementIPResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	checkSwitch(ctx, r.src, req.Plan, &resp.Diagnostics)
}
