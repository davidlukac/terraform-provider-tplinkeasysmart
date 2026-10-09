package provider

import (
	"context"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &stormDataSource{}
	_ datasource.DataSourceWithConfigure = &stormDataSource{}
)

// NewStormControlDataSource returns the tplinkeasysmart_storm_control data source.
func NewStormControlDataSource() datasource.DataSource { return &stormDataSource{} }

type stormDataSource struct{ client stormReader }

// stormReader is the part of the switch API this data source reads.
type stormReader interface {
	StormControl(ctx context.Context) ([]escp.PortStorm, error)
}

type portStormModel struct {
	Port           types.Int64 `tfsdk:"port"`
	Enabled        types.Bool  `tfsdk:"enabled"`
	UnknownUnicast types.Bool  `tfsdk:"unknown_unicast"`
	Multicast      types.Bool  `tfsdk:"multicast"`
	Broadcast      types.Bool  `tfsdk:"broadcast"`
	RateKbps       types.Int64 `tfsdk:"rate_kbps"`
}

type stormModel struct {
	ID    types.String     `tfsdk:"id"`
	Ports []portStormModel `tfsdk:"ports"`
}

func (d *stormDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storm_control"
}

func (d *stormDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	b := func(desc string) schema.BoolAttribute { return schema.BoolAttribute{Computed: true, Description: desc} }
	resp.Schema = schema.Schema{
		Description: "Per-port storm control (QoS > Storm Control). Read-only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `storm_control`."},
			"ports": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"port":            schema.Int64Attribute{Computed: true, Description: "Port number."},
				"enabled":         b("Storm control is active: a rate is set and at least one traffic type selected (as in the web UI)."),
				"unknown_unicast": b("Limit unknown unicast traffic."),
				"multicast":       b("Limit multicast traffic."),
				"broadcast":       b("Limit broadcast traffic."),
				"rate_kbps":       schema.Int64Attribute{Computed: true, Description: "Rate limit in kbps; 0 = unlimited."},
			}}},
		},
	}
}

func (d *stormDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom[stormReader](req.ProviderData, &resp.Diagnostics)
}

func (d *stormDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	rows, err := d.client.StormControl(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading storm control failed", err.Error())
		return
	}
	state := stormModel{ID: types.StringValue("storm_control"), Ports: make([]portStormModel, 0, len(rows))}
	for _, r := range rows {
		state.Ports = append(state.Ports, portStormModel{Port: types.Int64Value(int64(r.Port)), Enabled: types.BoolValue(r.Enabled),
			UnknownUnicast: types.BoolValue(r.UnknownUnicast), Multicast: types.BoolValue(r.Multicast),
			Broadcast: types.BoolValue(r.Broadcast), RateKbps: types.Int64Value(int64(r.RateKbps))})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
