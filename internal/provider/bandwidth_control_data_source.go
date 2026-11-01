package provider

import (
	"context"

	"github.com/davidlukac/escp-go"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &bandwidthDataSource{}
	_ datasource.DataSourceWithConfigure = &bandwidthDataSource{}
)

// NewBandwidthControlDataSource returns the tplinkeasysmart_bandwidth_control data source.
func NewBandwidthControlDataSource() datasource.DataSource { return &bandwidthDataSource{} }

type bandwidthDataSource struct {
	src    switchSource
	client bandwidthReader // set on the per-call copy made by bound()
	sw     types.String    // the switch that copy is bound to, for ids
}

// bandwidthReader is the part of the switch API this data source reads.
type bandwidthReader interface {
	Bandwidth(ctx context.Context) ([]escp.PortBandwidth, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *bandwidthDataSource) bound(add func(string, string), name types.String) *bandwidthDataSource {
	return bindSwitch(add, d.src, name, d, func(b *bandwidthDataSource, c bandwidthReader, sw types.String) { b.client, b.sw = c, sw })
}

type portBandwidthModel struct {
	Port        types.Int64 `tfsdk:"port"`
	IngressKbps types.Int64 `tfsdk:"ingress_kbps"`
	EgressKbps  types.Int64 `tfsdk:"egress_kbps"`
}

type bandwidthModel struct {
	Switch types.String         `tfsdk:"switch"`
	ID     types.String         `tfsdk:"id"`
	Ports  []portBandwidthModel `tfsdk:"ports"`
}

func (d *bandwidthDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_bandwidth_control"
}

func (d *bandwidthDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Per-port rate limits (QoS > Bandwidth Control). Read-only: a wrong limit on an uplink slows or cuts management access.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: dataSourceSwitchAttribute(),
			"id":       schema.StringAttribute{Computed: true, Description: "Always `bandwidth_control`."},
			"ports": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"port":         schema.Int64Attribute{Computed: true, Description: "Port number."},
				"ingress_kbps": schema.Int64Attribute{Computed: true, Description: "Ingress limit in kbps; 0 = unlimited."},
				"egress_kbps":  schema.Int64Attribute{Computed: true, Description: "Egress limit in kbps; 0 = unlimited."},
			}}},
		},
	}
}

func (d *bandwidthDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *bandwidthDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *bandwidthDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	rows, err := d.client.Bandwidth(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading bandwidth control failed", err.Error())
		return
	}
	state := bandwidthModel{ID: qualifiedID(d.sw, "bandwidth_control"), Ports: make([]portBandwidthModel, 0, len(rows))}
	for _, r := range rows {
		state.Ports = append(state.Ports, portBandwidthModel{Port: types.Int64Value(int64(r.Port)),
			IngressKbps: types.Int64Value(int64(r.Ingress)), EgressKbps: types.Int64Value(int64(r.Egress))})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
