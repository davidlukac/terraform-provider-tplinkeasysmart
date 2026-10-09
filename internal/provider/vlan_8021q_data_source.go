package provider

import (
	"context"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &vlan8021QDataSource{}
	_ datasource.DataSourceWithConfigure = &vlan8021QDataSource{}
)

// NewVLAN8021QDataSource returns the tplinkeasysmart_vlan_8021q data source.
func NewVLAN8021QDataSource() datasource.DataSource { return &vlan8021QDataSource{} }

type vlan8021QDataSource struct {
	src    switchSource
	client vlan8021QReader // set on the per-call copy made by bound()
	sw     types.String    // the switch that copy is bound to, for ids
}

// vlan8021QReader is the part of the switch API this data source reads.
type vlan8021QReader interface {
	VLAN8021Q(ctx context.Context) (escp.Dot1Q, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *vlan8021QDataSource) bound(add func(string, string), name types.String) *vlan8021QDataSource {
	return bindSwitch(add, d.src, name, d, func(b *vlan8021QDataSource, c vlan8021QReader, sw types.String) { b.client, b.sw = c, sw })
}

type vlan8021QVLANModel struct {
	ID       types.Int64  `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Tagged   []int64      `tfsdk:"tagged_ports"`
	Untagged []int64      `tfsdk:"untagged_ports"`
}

type pvidModel struct {
	Port types.Int64 `tfsdk:"port"`
	PVID types.Int64 `tfsdk:"pvid"`
}

type vlan8021QModel struct {
	Switch   types.String         `tfsdk:"switch"`
	ID       types.String         `tfsdk:"id"`
	Enabled  types.Bool           `tfsdk:"enabled"`
	MaxVLANs types.Int64          `tfsdk:"max_vlans"`
	VLANs    []vlan8021QVLANModel `tfsdk:"vlans"`
	PVIDs    []pvidModel          `tfsdk:"pvids"`
}

func (d *vlan8021QDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_8021q"
}

func (d *vlan8021QDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	ports := func(desc string) schema.ListAttribute {
		return schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "802.1Q VLAN mode, VLAN table and per-port PVIDs (VLAN > 802.1Q VLAN / 802.1Q PVID Setting). " +
			"Read-only: VLAN and PVID changes can cut management access. The VLAN table is only populated while the mode is enabled; the VLAN entry layout comes from the vendor utility and is untested on live data.",
		Attributes: map[string]schema.Attribute{
			attrSwitch:  dataSourceSwitchAttribute(),
			"id":        schema.StringAttribute{Computed: true, Description: "Always `vlan_8021q`."},
			"enabled":   schema.BoolAttribute{Computed: true, Description: "Whether 802.1Q VLAN mode is active."},
			"max_vlans": schema.Int64Attribute{Computed: true, Description: "Maximum number of VLANs."},
			"vlans": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":             schema.Int64Attribute{Computed: true, Description: "VLAN id."},
				"name":           schema.StringAttribute{Computed: true, Description: "VLAN name."},
				"tagged_ports":   ports("Ports carrying the VLAN tagged."),
				"untagged_ports": ports("Ports carrying the VLAN untagged."),
			}}},
			"pvids": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"port": schema.Int64Attribute{Computed: true, Description: "Port number."},
				"pvid": schema.Int64Attribute{Computed: true, Description: "Default VLAN for untagged frames on the port."},
			}}},
		},
	}
}

func (d *vlan8021QDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *vlan8021QDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *vlan8021QDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	v, err := d.client.VLAN8021Q(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading 802.1Q VLAN failed", err.Error())
		return
	}
	state := vlan8021QModel{ID: qualifiedID(d.sw, "vlan_8021q"), Enabled: types.BoolValue(v.Enabled), MaxVLANs: types.Int64Value(int64(v.MaxVLAN)),
		VLANs: make([]vlan8021QVLANModel, 0, len(v.VLANs)), PVIDs: make([]pvidModel, 0, len(v.PVIDs))}
	for _, g := range v.VLANs {
		state.VLANs = append(state.VLANs, vlan8021QVLANModel{ID: types.Int64Value(int64(g.ID)), Name: types.StringValue(g.Name),
			Tagged: toInt64s(g.Tagged), Untagged: toInt64s(g.Untagged)})
	}
	for _, p := range v.PVIDs {
		state.PVIDs = append(state.PVIDs, pvidModel{Port: types.Int64Value(int64(p.Port)), PVID: types.Int64Value(int64(p.PVID))})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
