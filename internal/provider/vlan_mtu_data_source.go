package provider

import (
	"context"

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

type vlanMTUDataSource struct{ client SwitchClient }

type vlanMTUModel struct {
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
			"id":          schema.StringAttribute{Computed: true, Description: "Always `vlan_mtu`."},
			"enabled":     schema.BoolAttribute{Computed: true, Description: "Whether MTU VLAN mode is active."},
			"uplink_port": schema.Int64Attribute{Computed: true, Description: "Uplink port (1 when the mode is disabled)."},
		},
	}
}

func (d *vlanMTUDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *vlanMTUDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	m, err := d.client.MTUVLAN(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading MTU VLAN failed", err.Error())
		return
	}
	state := vlanMTUModel{ID: types.StringValue("vlan_mtu"), Enabled: types.BoolValue(m.Enabled), UplinkPort: types.Int64Value(int64(m.UplinkPort))}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
