package provider

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &vlanMTUDataSource{}
	_ datasource.DataSourceWithConfigure = &vlanMTUDataSource{}
)

// NewVLANMTUDataSource returns the tplinkeasysmart_vlan_mtu data source.
func NewVLANMTUDataSource() datasource.DataSource { return &vlanMTUDataSource{} }

type vlanMTUDataSource struct {
	src    switchSource
	client vlanMTUReader // set on the per-call copy made by bound()
	sw     types.String  // the switch that copy is bound to, for ids
}

// vlanMTUReader is the part of the switch API this data source reads.
type vlanMTUReader interface {
	MTUVLAN(ctx context.Context) (escp.MTUVLAN, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *vlanMTUDataSource) bound(add func(string, string), name types.String) *vlanMTUDataSource {
	return bindSwitch(add, d.src, name, d, func(b *vlanMTUDataSource, c vlanMTUReader, sw types.String) { b.client, b.sw = c, sw })
}

type vlanMTUModel struct {
	Switch     types.String `tfsdk:"switch"`
	ID         types.String `tfsdk:"id"`
	Enabled    types.Bool   `tfsdk:"enabled"`
	UplinkPort types.Int64  `tfsdk:"uplink_port"`
}

func (d *vlanMTUDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_mtu"
}

func (d *vlanMTUDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "MTU VLAN mode (VLAN > MTU VLAN). Read-only. When enabled, all ports are isolated from each other and only reach the uplink port.",
		Attributes: map[string]schema.Attribute{
			attrSwitch:    dataSourceSwitchAttribute(),
			"id":          schema.StringAttribute{Computed: true, Description: "Always `vlan_mtu`."},
			"enabled":     schema.BoolAttribute{Computed: true, Description: "Whether MTU VLAN mode is active."},
			"uplink_port": schema.Int64Attribute{Computed: true, Description: "Uplink port (1 when the mode is disabled)."},
		},
	}
}

func (d *vlanMTUDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *vlanMTUDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *vlanMTUDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	m, err := d.client.MTUVLAN(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading MTU VLAN failed", err.Error())
		return
	}
	state := vlanMTUModel{ID: qualifiedID(d.sw, "vlan_mtu"), Enabled: types.BoolValue(m.Enabled), UplinkPort: types.Int64Value(int64(m.UplinkPort))}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
