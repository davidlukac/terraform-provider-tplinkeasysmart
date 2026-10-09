package provider

import (
	"context"

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

type qosDataSource struct{ client SwitchClient }

type portPriorityModel struct {
	Port     types.Int64 `tfsdk:"port"`
	Priority types.Int64 `tfsdk:"priority"`
}

type qosModel struct {
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
			"id":        schema.StringAttribute{Computed: true, Description: "Always `qos`."},
			"mode":      schema.Int64Attribute{Computed: true, Description: "0 port-based, 1 802.1p, 2 DSCP/802.1p."},
			"mode_name": schema.StringAttribute{Computed: true, Description: "port-based, 802.1p or dscp-802.1p."},
			"port_priorities": schema.ListNestedAttribute{Computed: true,
				Description: "Priority queue per port, 1 (lowest) to 4 (highest) as in the web UI. Only effective in port-based mode.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"port":     schema.Int64Attribute{Computed: true},
					"priority": schema.Int64Attribute{Computed: true},
				}}},
		},
	}
}

func (d *qosDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *qosDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	q, err := d.client.QoS(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading QoS failed", err.Error())
		return
	}
	state := qosModel{ID: types.StringValue("qos"), Mode: types.Int64Value(int64(q.Mode)), ModeName: types.StringValue(q.ModeName),
		Priorities: make([]portPriorityModel, 0, len(q.Priorities))}
	for _, p := range q.Priorities {
		state.Priorities = append(state.Priorities, portPriorityModel{Port: types.Int64Value(int64(p.Port)), Priority: types.Int64Value(int64(p.Priority))})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
