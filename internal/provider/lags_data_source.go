package provider

import (
	"context"

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

type lagsDataSource struct{ client SwitchClient }

type lagModel struct {
	ID    types.Int64 `tfsdk:"id"`
	Ports []int64     `tfsdk:"ports"`
}

type lagsModel struct {
	ID   types.String `tfsdk:"id"`
	LAGs []lagModel   `tfsdk:"lags"`
}

func (d *lagsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_lags"
}

func (d *lagsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Link aggregation groups (Switching > Port Trunk). Read-only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always `lags`."},
			"lags": schema.ListNestedAttribute{Computed: true, Description: "All groups the switch supports; unused groups have no ports.",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"id":    schema.Int64Attribute{Computed: true, Description: "Group number."},
					"ports": schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Member ports."},
				}}},
		},
	}
}

func (d *lagsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *lagsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	lags, err := d.client.LAGs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading LAGs failed", err.Error())
		return
	}
	state := lagsModel{ID: types.StringValue("lags"), LAGs: make([]lagModel, 0, len(lags))}
	for _, l := range lags {
		ports := make([]int64, 0, len(l.Ports))
		for _, p := range l.Ports {
			ports = append(ports, int64(p))
		}
		state.LAGs = append(state.LAGs, lagModel{ID: types.Int64Value(int64(l.ID)), Ports: ports})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
