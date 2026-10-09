package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &vlanPortBasedDataSource{}
	_ datasource.DataSourceWithConfigure = &vlanPortBasedDataSource{}
)

// NewVLANPortBasedDataSource returns the tplinkeasysmart_vlan_port_based data source.
func NewVLANPortBasedDataSource() datasource.DataSource { return &vlanPortBasedDataSource{} }

type vlanPortBasedDataSource struct{ client SwitchClient }

type portVLANModel struct {
	ID    types.Int64 `tfsdk:"id"`
	Ports []int64     `tfsdk:"ports"`
}

type vlanPortBasedModel struct {
	ID       types.String    `tfsdk:"id"`
	Enabled  types.Bool      `tfsdk:"enabled"`
	MaxVLANs types.Int64     `tfsdk:"max_vlans"`
	VLANs    []portVLANModel `tfsdk:"vlans"`
}

func (d *vlanPortBasedDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlan_port_based"
}

func (d *vlanPortBasedDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Port-based VLAN mode and groups (VLAN > Port Based VLAN). Read-only: VLAN changes can cut management access.",
		Attributes: map[string]schema.Attribute{
			"id":        schema.StringAttribute{Computed: true, Description: "Always `vlan_port_based`."},
			"enabled":   schema.BoolAttribute{Computed: true, Description: "Whether port-based VLAN mode is active."},
			"max_vlans": schema.Int64Attribute{Computed: true, Description: "Maximum number of VLAN groups."},
			"vlans": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":    schema.Int64Attribute{Computed: true, Description: "VLAN group id."},
				"ports": schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Member ports."},
			}}},
		},
	}
}

func (d *vlanPortBasedDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *vlanPortBasedDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	v, err := d.client.PortBasedVLAN(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading port-based VLAN failed", err.Error())
		return
	}
	state := vlanPortBasedModel{ID: types.StringValue("vlan_port_based"), Enabled: types.BoolValue(v.Enabled),
		MaxVLANs: types.Int64Value(int64(v.MaxVLAN)), VLANs: make([]portVLANModel, 0, len(v.VLANs))}
	for _, g := range v.VLANs {
		state.VLANs = append(state.VLANs, portVLANModel{ID: types.Int64Value(int64(g.ID)), Ports: toInt64s(g.Ports)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
