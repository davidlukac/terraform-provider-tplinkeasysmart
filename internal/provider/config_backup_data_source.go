package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/escp-go"
)

var (
	_ datasource.DataSource              = &configBackupDataSource{}
	_ datasource.DataSourceWithConfigure = &configBackupDataSource{}
)

// NewConfigBackupDataSource returns the tplinkeasysmart_config_backup data source.
func NewConfigBackupDataSource() datasource.DataSource { return &configBackupDataSource{} }

type configBackupDataSource struct {
	src    switchSource
	client configBackupReader // set on the per-call copy made by bound()
	sw     types.String       // the switch that copy is bound to, for ids
}

// configBackupReader is the part of the switch API this data source reads.
type configBackupReader interface {
	ConfigBackup(ctx context.Context) (escp.ConfigFile, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *configBackupDataSource) bound(add func(string, string), name types.String) *configBackupDataSource {
	return bindSwitch(add, d.src, name, d, func(b *configBackupDataSource, c configBackupReader, sw types.String) { b.client, b.sw = c, sw })
}

type configBackupModel struct {
	Switch         types.String `tfsdk:"switch"`
	ID             types.String `tfsdk:"id"`
	IncludeContent types.Bool   `tfsdk:"include_content"`
	Size           types.Int64  `tfsdk:"size"`
	SHA256         types.String `tfsdk:"sha256"`
	ContentBase64  types.String `tfsdk:"content_base64"`
}

func (d *configBackupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_config_backup"
}

func (d *configBackupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The switch's binary configuration file (System > System Tools > Backup Config), read over the management protocol. Read-only: nothing on the switch changes. " +
			"By default only size and SHA-256 are returned, which is enough to detect configuration changes made outside Terraform. " +
			"The file contains an obfuscated credential block; set `include_content` only if you need the bytes, and treat the state as secret.",
		Attributes: map[string]schema.Attribute{
			attrSwitch:        dataSourceSwitchAttribute(),
			"id":              schema.StringAttribute{Computed: true, Description: "Always `config_backup`."},
			"include_content": schema.BoolAttribute{Optional: true, Description: "Also return the file as base64 in `content_base64`. Defaults to false."},
			"size":            schema.Int64Attribute{Computed: true, Description: "File size in bytes."},
			"sha256":          schema.StringAttribute{Computed: true, Description: "Hex SHA-256 of the file."},
			"content_base64":  schema.StringAttribute{Computed: true, Sensitive: true, Description: "The file, base64 encoded; null unless `include_content` is true."},
		},
	}
}

func (d *configBackupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *configBackupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *configBackupDataSource) readBound(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg configBackupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if !resp.Diagnostics.HasError() {
		d.read(ctx, cfg, resp)
	}
}

func (d *configBackupDataSource) read(ctx context.Context, cfg configBackupModel, resp *datasource.ReadResponse) {
	data, err := d.client.ConfigBackup(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading the configuration backup failed", err.Error())
		return
	}
	state := configBackupModel{
		ID: qualifiedID(d.sw, "config_backup"), IncludeContent: cfg.IncludeContent,
		Size: types.Int64Value(int64(data.Size())), SHA256: types.StringValue(data.SHA256()),
		ContentBase64: types.StringNull(),
	}
	if cfg.IncludeContent.ValueBool() {
		state.ContentBase64 = types.StringValue(data.Base64())
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
