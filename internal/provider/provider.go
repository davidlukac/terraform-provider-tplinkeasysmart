package provider

import (
	"context"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

var _ provider.Provider = &tplinkProvider{}

type tplinkProvider struct {
	version string
	// newClient builds the transport; tests replace it with a fake.
	newClient func(escp.Config) (SwitchClient, error)
}

// New returns the provider factory used by main.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &tplinkProvider{version: version, newClient: func(c escp.Config) (SwitchClient, error) { return escp.Dial(c) }}
	}
}

type providerModel struct {
	Host           types.String `tfsdk:"host"`
	Username       types.String `tfsdk:"username"`
	Password       types.String `tfsdk:"password"`
	TimeoutSeconds types.Int64  `tfsdk:"timeout_seconds"`
}

func (p *tplinkProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "tplinkeasysmart"
	resp.Version = p.version
}

func (p *tplinkProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages TP-Link Easy Smart switches (TL-SG108E v6) over their native UDP management protocol. " +
			"The provider must run on the same layer-2 network as the switch, and only one switch per Terraform run is supported " +
			"(the switch always replies to UDP port 29809).",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Optional:    true,
				Description: "IPv4 address or hostname of the switch. Falls back to TPLINK_EASYSMART_HOST.",
			},
			"username": schema.StringAttribute{
				Optional:    true,
				Description: "Admin user name. Falls back to TPLINK_EASYSMART_USERNAME.",
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Admin password. Falls back to TPLINK_EASYSMART_PASSWORD.",
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:    true,
				Description: "Seconds to wait for a reply before retrying a request. Defaults to 3.",
			},
		},
	}
}

func envOr(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func (p *tplinkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.Host.IsUnknown() || cfg.Username.IsUnknown() || cfg.Password.IsUnknown() {
		return // values not known yet during planning
	}
	host := envOr(cfg.Host, "TPLINK_EASYSMART_HOST")
	user := envOr(cfg.Username, "TPLINK_EASYSMART_USERNAME")
	pass := envOr(cfg.Password, "TPLINK_EASYSMART_PASSWORD")
	if host == "" {
		resp.Diagnostics.AddAttributeError(pathRoot("host"), "Missing switch host", "Set `host` or TPLINK_EASYSMART_HOST.")
	}
	if user == "" || pass == "" {
		resp.Diagnostics.AddError("Missing credentials", "Set `username`/`password` or TPLINK_EASYSMART_USERNAME/TPLINK_EASYSMART_PASSWORD.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	timeout := 3 * time.Second
	if !cfg.TimeoutSeconds.IsNull() && cfg.TimeoutSeconds.ValueInt64() > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds.ValueInt64()) * time.Second
	}
	c, err := p.newClient(escp.Config{Host: host, Username: user, Password: pass, Timeout: timeout})
	if err != nil {
		resp.Diagnostics.AddError("Cannot configure switch client", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *tplinkProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewLEDResource, NewIGMPSnoopingResource}
}

func (p *tplinkProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewSystemInfoDataSource, NewPortsDataSource, NewPortStatisticsDataSource, NewLAGsDataSource}
}
