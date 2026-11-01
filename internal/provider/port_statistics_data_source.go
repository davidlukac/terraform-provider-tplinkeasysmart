package provider

import (
	"context"

	"github.com/davidlukac/escp-go"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &portStatisticsDataSource{}
	_ datasource.DataSourceWithConfigure = &portStatisticsDataSource{}
)

// NewPortStatisticsDataSource returns the tplinkeasysmart_port_statistics data source.
func NewPortStatisticsDataSource() datasource.DataSource { return &portStatisticsDataSource{} }

type portStatisticsDataSource struct {
	src    switchSource
	client portStatisticsReader // set on the per-call copy made by bound()
	sw     types.String         // the switch that copy is bound to, for ids
}

// portStatisticsReader is the part of the switch API this data source reads.
type portStatisticsReader interface {
	PortStats(ctx context.Context) ([]escp.PortStats, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *portStatisticsDataSource) bound(add func(string, string), name types.String) *portStatisticsDataSource {
	return bindSwitch(add, d.src, name, d, func(b *portStatisticsDataSource, c portStatisticsReader, sw types.String) { b.client, b.sw = c, sw })
}

type portStatsModel struct {
	ID      types.Int64  `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Link    types.String `tfsdk:"link"`
	TxGood  types.Int64  `tfsdk:"tx_good_packets"`
	TxBad   types.Int64  `tfsdk:"tx_bad_packets"`
	RxGood  types.Int64  `tfsdk:"rx_good_packets"`
	RxBad   types.Int64  `tfsdk:"rx_bad_packets"`
}

type portStatisticsModel struct {
	Switch types.String     `tfsdk:"switch"`
	ID     types.String     `tfsdk:"id"`
	Ports  []portStatsModel `tfsdk:"ports"`
}

func (d *portStatisticsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_statistics"
}

func (d *portStatisticsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	i := func(desc string) schema.Int64Attribute {
		return schema.Int64Attribute{Computed: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "Per-port traffic counters (Monitoring > Port Statistics). Read-only; values change on every read, so do not use them in resource arguments. Counters are 32-bit and wrap.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: dataSourceSwitchAttribute(),
			"id":       schema.StringAttribute{Computed: true, Description: "Always `port_statistics`."},
			"ports": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":              i("Port number (1-based)."),
				"enabled":         schema.BoolAttribute{Computed: true, Description: "Administrative state."},
				"link":            schema.StringAttribute{Computed: true, Description: "link-down or the negotiated speed."},
				"tx_good_packets": i("Good packets transmitted."),
				"tx_bad_packets":  i("Bad packets transmitted."),
				"rx_good_packets": i("Good packets received."),
				"rx_bad_packets":  i("Bad packets received."),
			}}},
		},
	}
}

func (d *portStatisticsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *portStatisticsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *portStatisticsDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	rows, err := d.client.PortStats(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading port statistics failed", err.Error())
		return
	}
	state := portStatisticsModel{ID: qualifiedID(d.sw, "port_statistics"), Ports: make([]portStatsModel, 0, len(rows))}
	for _, r := range rows {
		state.Ports = append(state.Ports, portStatsModel{
			ID: types.Int64Value(int64(r.ID)), Enabled: types.BoolValue(r.Enabled), Link: types.StringValue(r.Link.String()),
			TxGood: types.Int64Value(int64(r.TxGood)), TxBad: types.Int64Value(int64(r.TxBad)),
			RxGood: types.Int64Value(int64(r.RxGood)), RxBad: types.Int64Value(int64(r.RxBad)),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
