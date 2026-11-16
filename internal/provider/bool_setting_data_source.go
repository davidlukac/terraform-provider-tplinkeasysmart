package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// boolSettingSpec is everything that tells the data sources of single on/off settings apart (the LED, loop
// prevention); C is the part of the switch API the data source reads with.
type boolSettingSpec[C any] struct {
	suffix      string // the type name after the provider name
	description string // the description of the data source
	enabledDesc string // the description of `enabled`
	readFailure string // the summary of the diagnostic when the switch cannot be read
	id          string // what `id` is, before the switch name qualifies it
	readSetting func(ctx context.Context, c C) (bool, error)
}

// boolSettingDataSource reads one on/off setting of the switch: `enabled`, `id` and the `switch` argument,
// the same three attributes as the resource of that setting minus its write-side arguments. It has no
// write path.
type boolSettingDataSource[C any] struct {
	spec   boolSettingSpec[C]
	src    switchSource
	client C            // set on the per-call copy made by bound()
	sw     types.String // the switch that copy is bound to, for ids
}

var (
	_ datasource.DataSource              = &boolSettingDataSource[ledReader]{}
	_ datasource.DataSourceWithConfigure = &boolSettingDataSource[ledReader]{}
)

// boolSettingModel is the state of such a data source.
type boolSettingModel struct {
	Switch  types.String `tfsdk:"switch"`
	ID      types.String `tfsdk:"id"`
	Enabled types.Bool   `tfsdk:"enabled"`
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *boolSettingDataSource[C]) bound(add func(string, string), name types.String) *boolSettingDataSource[C] {
	return bindSwitch(add, d.src, name, d, func(b *boolSettingDataSource[C], c C, sw types.String) { b.client, b.sw = c, sw })
}

func (d *boolSettingDataSource[C]) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.spec.suffix
}

func (d *boolSettingDataSource[C]) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: d.spec.description + " Read-only; set it with the `tplinkeasysmart_" + d.spec.suffix + "` resource.",
		Attributes: map[string]schema.Attribute{
			attrSwitch: dataSourceSwitchAttribute(),
			"id":       schema.StringAttribute{Computed: true, Description: "Always `" + d.spec.id + "`."},
			"enabled":  schema.BoolAttribute{Computed: true, Description: d.spec.enabledDesc},
		},
	}
}

func (d *boolSettingDataSource[C]) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *boolSettingDataSource[C]) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *boolSettingDataSource[C]) readBound(ctx context.Context, resp *datasource.ReadResponse) {
	on, err := d.spec.readSetting(ctx, d.client)
	if err != nil {
		resp.Diagnostics.AddError(d.spec.readFailure, err.Error())
		return
	}
	state := boolSettingModel{ID: qualifiedID(d.sw, d.spec.id), Enabled: types.BoolValue(on)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
