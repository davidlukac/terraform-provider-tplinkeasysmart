package provider

import (
	"context"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &portMirrorDataSource{}
	_ datasource.DataSourceWithConfigure = &portMirrorDataSource{}
)

// NewPortMirrorDataSource returns the tplinkeasysmart_port_mirror data source.
func NewPortMirrorDataSource() datasource.DataSource { return &portMirrorDataSource{} }

type portMirrorDataSource struct {
	src    switchSource
	client portMirrorReader // set on the per-call copy made by bound()
	sw     types.String     // the switch that copy is bound to, for ids
}

// portMirrorReader is the part of the switch API this data source reads.
type portMirrorReader interface {
	PortMirror(ctx context.Context) (escp.Mirror, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *portMirrorDataSource) bound(add func(string, string), name types.String) *portMirrorDataSource {
	return bindSwitch(add, d.src, name, d, func(b *portMirrorDataSource, c portMirrorReader, sw types.String) { b.client, b.sw = c, sw })
}

type portMirrorModel struct {
	Switch       types.String `tfsdk:"switch"`
	ID           types.String `tfsdk:"id"`
	Enabled      types.Bool   `tfsdk:"enabled"`
	MirrorPort   types.Int64  `tfsdk:"mirror_port"`
	IngressPorts []int64      `tfsdk:"ingress_ports"`
	EgressPorts  []int64      `tfsdk:"egress_ports"`
}

func (d *portMirrorDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_port_mirror"
}

func (d *portMirrorDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	list := func(desc string) schema.ListAttribute {
		return schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "Port mirroring (Monitoring > Port Mirror). Read-only: enabling mirroring turns the destination port into a monitor port and can cut its normal traffic.",
		Attributes: map[string]schema.Attribute{
			attrSwitch:      dataSourceSwitchAttribute(),
			"id":            schema.StringAttribute{Computed: true, Description: "Always `port_mirror`."},
			"enabled":       schema.BoolAttribute{Computed: true, Description: "Whether mirroring is enabled."},
			"mirror_port":   schema.Int64Attribute{Computed: true, Description: "Destination (monitoring) port; 0 when disabled."},
			"ingress_ports": list("Ports whose received traffic is mirrored."),
			"egress_ports":  list("Ports whose transmitted traffic is mirrored."),
		},
	}
}

func (d *portMirrorDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

func toInt64s(in []int) []int64 {
	out := make([]int64, 0, len(in))
	for _, v := range in {
		out = append(out, int64(v))
	}
	return out
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *portMirrorDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *portMirrorDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	m, err := d.client.PortMirror(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading port mirror failed", err.Error())
		return
	}
	state := portMirrorModel{ID: qualifiedID(d.sw, "port_mirror"), Enabled: types.BoolValue(m.Enabled),
		MirrorPort: types.Int64Value(int64(m.MirrorPort)), IngressPorts: toInt64s(m.IngressPorts), EgressPorts: toInt64s(m.EgressPorts)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
