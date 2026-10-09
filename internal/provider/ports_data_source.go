package provider

import (
	"context"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &portsDataSource{}
	_ datasource.DataSourceWithConfigure = &portsDataSource{}
)

// NewPortsDataSource returns the tplinkeasysmart_ports data source.
func NewPortsDataSource() datasource.DataSource { return &portsDataSource{} }

type portsDataSource struct {
	src    switchSource
	client portsReader  // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// portsReader is the part of the switch API this data source reads.
type portsReader interface {
	Ports(ctx context.Context) ([]escp.Port, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *portsDataSource) bound(add func(string, string), name types.String) *portsDataSource {
	return bindSwitch(add, d.src, name, d, func(b *portsDataSource, c portsReader, sw types.String) { b.client, b.sw = c, sw })
}

type portModel struct {
	ID                    types.Int64  `tfsdk:"id"`
	Enabled               types.Bool   `tfsdk:"enabled"`
	LAG                   types.Int64  `tfsdk:"lag"`
	ConfiguredSpeed       types.String `tfsdk:"configured_speed"`
	ActualSpeed           types.String `tfsdk:"actual_speed"`
	ConfiguredFlowControl types.Bool   `tfsdk:"configured_flow_control"`
	ActualFlowControl     types.Bool   `tfsdk:"actual_flow_control"`
}

type portsModel struct {
	Switch types.String `tfsdk:"switch"`
	ID     types.String `tfsdk:"id"`
	Ports  []portModel  `tfsdk:"ports"`
}

func (d *portsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ports"
}

func (d *portsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Port settings and link state (Switching > Port Setting in the web UI). Read-only.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: dataSourceSwitchAttribute(),
			"id":       schema.StringAttribute{Computed: true, Description: "Always `ports`."},
			"ports": schema.ListNestedAttribute{
				Computed:    true,
				Description: "One entry per physical port.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":                      schema.Int64Attribute{Computed: true, Description: "Port number (1-based)."},
					"enabled":                 schema.BoolAttribute{Computed: true, Description: "Administrative state."},
					"lag":                     schema.Int64Attribute{Computed: true, Description: "LAG group the port belongs to, 0 if none."},
					"configured_speed":        schema.StringAttribute{Computed: true, Description: "auto, 10Mbps-half, 10Mbps-full, 100Mbps-half, 100Mbps-full or 1000Mbps-full."},
					"actual_speed":            schema.StringAttribute{Computed: true, Description: "Negotiated speed, or link-down."},
					"configured_flow_control": schema.BoolAttribute{Computed: true, Description: "Configured flow control."},
					"actual_flow_control":     schema.BoolAttribute{Computed: true, Description: "Negotiated flow control."},
				}},
			},
		},
	}
}

func (d *portsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *portsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *portsDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	ports, err := d.client.Ports(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading ports failed", err.Error())
		return
	}
	state := portsModel{ID: qualifiedID(d.sw, "ports"), Ports: make([]portModel, 0, len(ports))}
	for _, p := range ports {
		state.Ports = append(state.Ports, portModel{
			ID: types.Int64Value(int64(p.ID)), Enabled: types.BoolValue(p.Enabled), LAG: types.Int64Value(int64(p.LAG)),
			ConfiguredSpeed: types.StringValue(p.ConfiguredSpeed), ActualSpeed: types.StringValue(p.ActualSpeed),
			ConfiguredFlowControl: types.BoolValue(p.ConfiguredFlowControl), ActualFlowControl: types.BoolValue(p.ActualFlowControl),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
