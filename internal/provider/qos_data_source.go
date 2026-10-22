package provider

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &qosDataSource{}
	_ datasource.DataSourceWithConfigure = &qosDataSource{}
)

// NewQoSDataSource returns the tplinkeasysmart_qos data source.
func NewQoSDataSource() datasource.DataSource { return &qosDataSource{} }

type qosDataSource struct {
	src    switchSource
	client qosReader    // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// qosReader is the part of the switch API this data source reads.
type qosReader interface {
	QoS(ctx context.Context) (escp.QoS, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *qosDataSource) bound(add func(string, string), name types.String) *qosDataSource {
	return bindSwitch(add, d.src, name, d, func(b *qosDataSource, c qosReader, sw types.String) { b.client, b.sw = c, sw })
}

type portPriorityModel struct {
	Port     types.Int64 `tfsdk:"port"`
	Priority types.Int64 `tfsdk:"priority"`
}

type qosModel struct {
	Switch     types.String        `tfsdk:"switch"`
	ID         types.String        `tfsdk:"id"`
	Mode       types.Int64         `tfsdk:"mode"`
	ModeName   types.String        `tfsdk:"mode_name"`
	Priorities []portPriorityModel `tfsdk:"port_priorities"`
}

func (d *qosDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_qos"
}

func (d *qosDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "QoS basic settings (QoS > QoS Basic). Read-only.",
		Attributes: map[string]schema.Attribute{
			attrSwitch:  dataSourceSwitchAttribute(),
			"id":        schema.StringAttribute{Computed: true, Description: "Always `qos`."},
			"mode":      schema.Int64Attribute{Computed: true, Description: "0 port-based, 1 802.1p, 2 DSCP/802.1p."},
			"mode_name": schema.StringAttribute{Computed: true, Description: "port-based, 802.1p or dscp-802.1p."},
			"port_priorities": schema.ListNestedAttribute{Computed: true,
				Description: "Priority queue per port, 1 (lowest) to 4 (highest) as in the web UI. Only effective in port-based mode.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"port":     schema.Int64Attribute{Computed: true, Description: "Port number."},
					"priority": schema.Int64Attribute{Computed: true, Description: "Priority queue, 1 (lowest) to 4 (highest)."},
				}}},
		},
	}
}

func (d *qosDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *qosDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *qosDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	q, err := d.client.QoS(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading QoS failed", err.Error())
		return
	}
	state := qosModel{ID: qualifiedID(d.sw, "qos"), Mode: types.Int64Value(int64(q.Mode)), ModeName: types.StringValue(q.Mode.String()),
		Priorities: make([]portPriorityModel, 0, len(q.Priorities))}
	for _, p := range q.Priorities {
		state.Priorities = append(state.Priorities, portPriorityModel{Port: types.Int64Value(int64(p.Port)), Priority: types.Int64Value(int64(p.Priority))})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
