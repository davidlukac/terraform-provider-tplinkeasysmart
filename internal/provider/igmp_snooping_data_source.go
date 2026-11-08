package provider

import (
	"context"

	"github.com/davidlukac/escp-go/model"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &igmpDataSource{}
	_ datasource.DataSourceWithConfigure = &igmpDataSource{}
)

// NewIGMPSnoopingDataSource returns the tplinkeasysmart_igmp_snooping data source.
func NewIGMPSnoopingDataSource() datasource.DataSource { return &igmpDataSource{} }

type igmpDataSource struct {
	src    switchSource
	client igmpReader   // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

// igmpReader is the part of the switch API this data source reads.
type igmpReader interface {
	IGMP(ctx context.Context) (model.IGMP, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *igmpDataSource) bound(add func(string, string), name types.String) *igmpDataSource {
	return bindSwitch(add, d.src, name, d, func(b *igmpDataSource, c igmpReader, sw types.String) { b.client, b.sw = c, sw })
}

type igmpDataSourceModel struct {
	Switch            types.String `tfsdk:"switch"`
	ID                types.String `tfsdk:"id"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	ReportSuppression types.Bool   `tfsdk:"report_message_suppression"`
	Groups            types.List   `tfsdk:"multicast_groups"`
}

func (d *igmpDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_igmp_snooping"
}

func (d *igmpDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "IGMP snooping settings and the multicast groups the switch has learned (Switching > IGMP Snooping). Read-only; " +
			"the groups change at runtime, so keep them out of anything compared between plans. Set the two switches with the `tplinkeasysmart_igmp_snooping` resource.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: dataSourceSwitchAttribute(),
			"id":       schema.StringAttribute{Computed: true, Description: "Always `igmp_snooping`."},
			"enabled":  schema.BoolAttribute{Computed: true, Description: "Whether IGMP snooping is enabled."},
			"report_message_suppression": schema.BoolAttribute{Computed: true,
				Description: "Whether IGMP report message suppression is enabled."},
			"multicast_groups": schema.ListNestedAttribute{Computed: true, Description: "Multicast groups currently learned (changes at runtime).",
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"ip":    schema.StringAttribute{Computed: true, Description: "Multicast group address."},
					"vlan":  schema.Int64Attribute{Computed: true, Description: "VLAN the group was learned in."},
					"ports": schema.ListAttribute{Computed: true, ElementType: types.Int64Type, Description: "Ports with subscribers."},
				}}},
		},
	}
}

func (d *igmpDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *igmpDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

var igmpGroupType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"ip": types.StringType, "vlan": types.Int64Type, "ports": types.ListType{ElemType: types.Int64Type},
}}

func (d *igmpDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	s, err := d.client.IGMP(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading IGMP snooping failed", err.Error())
		return
	}
	// The values are built from the group type itself, so they cannot violate it.
	groups := make([]attr.Value, 0, len(s.Groups))
	for _, g := range s.Groups {
		ports := make([]attr.Value, 0, g.Ports.Len())
		for _, p := range g.Ports.Ports() {
			ports = append(ports, types.Int64Value(int64(p)))
		}
		groups = append(groups, types.ObjectValueMust(igmpGroupType.AttrTypes, map[string]attr.Value{
			"ip":    types.StringValue(g.IP.String()),
			"vlan":  types.Int64Value(int64(g.VLAN)),
			"ports": types.ListValueMust(types.Int64Type, ports),
		}))
	}
	state := igmpDataSourceModel{ID: qualifiedID(d.sw, igmpID), Enabled: types.BoolValue(s.Enabled),
		ReportSuppression: types.BoolValue(s.ReportSuppression), Groups: types.ListValueMust(igmpGroupType, groups)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
