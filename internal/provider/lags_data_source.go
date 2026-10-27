package provider

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &lagsDataSource{}
	_ datasource.DataSourceWithConfigure = &lagsDataSource{}
)

// NewLAGsDataSource returns the tplinkeasysmart_lags data source.
func NewLAGsDataSource() datasource.DataSource { return &lagsDataSource{} }

type lagsDataSource struct {
	src    switchSource
	client lagsReader   // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// lagsReader is the part of the switch API this data source reads.
type lagsReader interface {
	LAGs(ctx context.Context) ([]escp.LAG, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *lagsDataSource) bound(add func(string, string), name types.String) *lagsDataSource {
	return bindSwitch(add, d.src, name, d, func(b *lagsDataSource, c lagsReader, sw types.String) { b.client, b.sw = c, sw })
}

type lagModel struct {
	ID    types.Int64 `tfsdk:"id"`
	Ports []int64     `tfsdk:"ports"`
}

type lagsModel struct {
	Switch types.String `tfsdk:"switch"`
	ID     types.String `tfsdk:"id"`
	LAGs   []lagModel   `tfsdk:"lags"`
}

func (d *lagsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lags"
}

func (d *lagsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Link aggregation groups (Switching > Port Trunk). Read-only.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: dataSourceSwitchAttribute(),
			"id":       schema.StringAttribute{Computed: true, Description: "Always `lags`."},
			"lags": schema.ListNestedAttribute{Computed: true, Description: "All groups the switch supports; unused groups have no ports.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":    schema.Int64Attribute{Computed: true, Description: "Group number."},
					"ports": schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Member ports."},
				}}},
		},
	}
}

func (d *lagsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *lagsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *lagsDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	lags, err := d.client.LAGs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading LAGs failed", err.Error())
		return
	}
	state := lagsModel{ID: qualifiedID(d.sw, "lags"), LAGs: make([]lagModel, 0, len(lags))}
	for _, l := range lags {
		state.LAGs = append(state.LAGs, lagModel{ID: types.Int64Value(int64(l.ID)), Ports: portInt64s(l.Ports)})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
