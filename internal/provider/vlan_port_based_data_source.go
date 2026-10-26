package provider

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

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

type vlanPortBasedDataSource struct {
	src    switchSource
	client vlanPortBasedReader // set on the per-call copy made by bound()
	sw     types.String        // the switch that copy is bound to, for ids
}

// vlanPortBasedReader is the part of the switch API this data source reads.
type vlanPortBasedReader interface {
	PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *vlanPortBasedDataSource) bound(add func(string, string), name types.String) *vlanPortBasedDataSource {
	return bindSwitch(add, d.src, name, d, func(b *vlanPortBasedDataSource, c vlanPortBasedReader, sw types.String) { b.client, b.sw = c, sw })
}

type portVLANModel struct {
	ID    types.Int64 `tfsdk:"id"`
	Ports []int64     `tfsdk:"ports"`
}

type vlanPortBasedModel struct {
	Switch   types.String    `tfsdk:"switch"`
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
			attrSwitch:  dataSourceSwitchAttribute(),
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
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *vlanPortBasedDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *vlanPortBasedDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	v, err := d.client.PortBasedVLAN(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading port-based VLAN failed", err.Error())
		return
	}
	state := vlanPortBasedModel{ID: qualifiedID(d.sw, "vlan_port_based"), Enabled: types.BoolValue(v.Enabled),
		MaxVLANs: types.Int64Value(int64(v.MaxVLAN)), VLANs: make([]portVLANModel, 0, len(v.VLANs))}
	for _, g := range v.VLANs {
		state.VLANs = append(state.VLANs, portVLANModel{ID: types.Int64Value(int64(g.ID)), Ports: portInt64s(g.Ports)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
