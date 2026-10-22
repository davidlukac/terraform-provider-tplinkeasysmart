package provider

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
)

// Environment variables that back the provider arguments.
const (
	envHost     = "TPLINK_EASYSMART_HOST"
	envUsername = "TPLINK_EASYSMART_USERNAME"
	envPassword = "TPLINK_EASYSMART_PASSWORD" //nolint:gosec // the name of an environment variable, not a credential
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
	Switches       types.Map    `tfsdk:"switches"`
	// AllowUnverifiedFirmware lifts the write refusal on firmware builds this provider was not verified against.
	AllowUnverifiedFirmware types.Bool `tfsdk:"allow_unverified_firmware"`
}

// switchModel is one entry of the `switches` map.
type switchModel struct {
	Host                    types.String `tfsdk:"host"`
	Username                types.String `tfsdk:"username"`
	Password                types.String `tfsdk:"password"`
	TimeoutSeconds          types.Int64  `tfsdk:"timeout_seconds"`
	AllowUnverifiedFirmware types.Bool   `tfsdk:"allow_unverified_firmware"`
}

func (p *tplinkProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "tplinkeasysmart"
	resp.Version = p.version
}

func (p *tplinkProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages TP-Link Easy Smart switches (TL-SG108E v6) over their native UDP management protocol. " +
			"The provider must run on the same layer-2 network as the switches. Configure one switch with `host`, `username` and `password` " +
			"(use a provider alias per switch), or any number with the `switches` map and select one per resource with `switch`. " +
			"Do not run two clients against the same switch at once.",
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
				Validators:  []validator.Int64{int64validator.AtLeast(1)},
				Description: "Seconds to wait for a reply before retrying a request. Defaults to 3.",
			},
			"allow_unverified_firmware": schema.BoolAttribute{
				Optional: true,
				Description: "Write to switches whose firmware build this provider version was not verified against. Defaults to false: reads and imports always work, " +
					"but every write is refused on an unverified build, because a newer firmware may have changed the protocol. Verified builds: " + verifiedBuilds() + ". " +
					"Can be overridden per switch in the `switches` map.",
			},
			"switches": schema.MapNestedAttribute{
				Optional: true,
				Description: "Switches managed by this provider, by name. Select one on a resource or data source with `switch = \"<name>\"`. " +
					"May be combined with `host`, which then names the default switch used when `switch` is omitted.",
				Validators: []validator.Map{mapvalidator.KeysAre(validSwitchName()...)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"host":     schema.StringAttribute{Required: true, Description: "IPv4 address or hostname of the switch."},
					"username": schema.StringAttribute{Required: true, Description: "Admin user name."},
					"password": schema.StringAttribute{Required: true, Sensitive: true, Description: "Admin password."},
					"timeout_seconds": schema.Int64Attribute{Optional: true, Validators: []validator.Int64{int64validator.AtLeast(1)},
						Description: "Seconds to wait for a reply before retrying a request. Defaults to the provider-level `timeout_seconds`, then 3."},
					"allow_unverified_firmware": schema.BoolAttribute{Optional: true,
						Description: "Overrides the provider-level `allow_unverified_firmware` for this switch."},
				}},
			},
		},
	}
}

// verifiedBuilds lists the verified firmware builds for the attribute description.
func verifiedBuilds() string { return strings.Join(safety.VerifiedFirmwareBuilds(), ", ") }

func envOr(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

// defaultTimeout is the per-request timeout when none is configured.
const defaultTimeout = 3 * time.Second

func timeoutOr(configured types.Int64, fallback time.Duration) time.Duration {
	if !configured.IsNull() && configured.ValueInt64() > 0 {
		return time.Duration(configured.ValueInt64()) * time.Second
	}
	return fallback
}

// Configure builds the registry of switches: the provider's own switch (host,
// username, password, with environment fallbacks) as the default, and every
// entry of the `switches` map by name. Clients are created lazily; nothing
// touches the network here.
func (p *tplinkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A value that is only known after apply (for example taken from another
	// resource) cannot be used to build a client during planning. Without a
	// client every resource and data source would dereference nil, so fail
	// with an explanation instead.
	for _, attr := range []struct {
		name  string
		value attr.Value
	}{{"host", cfg.Host}, {"username", cfg.Username}, {"password", cfg.Password}, {"switches", cfg.Switches}} {
		if attr.value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(pathRoot(attr.name), "Unknown switch "+attr.name,
				"The provider cannot create the switch client because `"+attr.name+"` is not known yet. Apply the resource that provides it first "+
					"(`terraform apply -target=...`), set the value statically, or use the TPLINK_EASYSMART_"+strings.ToUpper(attr.name)+" environment variable.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	var entries map[string]switchModel
	resp.Diagnostics.Append(cfg.Switches.ElementsAs(ctx, &entries, false)...)
	for name, e := range entries {
		for _, f := range []struct {
			attr  string
			value attr.Value
		}{{"host", e.Host}, {"username", e.Username}, {"password", e.Password}} {
			if f.value.IsUnknown() {
				resp.Diagnostics.AddAttributeError(pathRoot("switches").AtMapKey(name).AtName(f.attr), "Unknown switch "+f.attr,
					"The provider cannot create the client for switch "+name+" because `"+f.attr+"` is not known yet; set it statically or apply the resource that provides it first.")
			}
		}
	}
	host := envOr(cfg.Host, envHost)
	user := envOr(cfg.Username, envUsername)
	pass := envOr(cfg.Password, envPassword)
	hasDefault := host != "" || len(entries) == 0
	if hasDefault && host == "" {
		resp.Diagnostics.AddAttributeError(pathRoot("host"), "Missing switch host", "Set `host` or TPLINK_EASYSMART_HOST, or define switches with `switches`.")
	}
	if hasDefault && (user == "" || pass == "") {
		resp.Diagnostics.AddError("Missing credentials", "Set `username`/`password` or TPLINK_EASYSMART_USERNAME/TPLINK_EASYSMART_PASSWORD.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	providerTimeout := timeoutOr(cfg.TimeoutSeconds, defaultTimeout)
	reg := &switchRegistry{named: map[string]SwitchClient{}}
	hosts := map[string]string{} // host -> switch, so one device is never configured twice
	providerAllow := cfg.AllowUnverifiedFirmware.ValueBool()
	build := func(name, h, u, pw string, timeout time.Duration, allowUnverified bool) {
		label := name
		if label == "" {
			label = "the provider's own switch"
		}
		if other, dup := hosts[h]; dup {
			resp.Diagnostics.AddError("Same switch configured twice", fmt.Sprintf("%s and %s have the same host %q; two clients on one switch would compete for it.", other, label, h))
			return
		}
		hosts[h] = label
		c, err := p.newClient(escp.Config{Host: h, Username: u, Password: pw, Timeout: timeout, WriteGate: firmwareWriteGate(allowUnverified)})
		if err != nil {
			resp.Diagnostics.AddError("Cannot configure switch client", fmt.Sprintf("%s: %s", label, err))
			return
		}
		if name == "" {
			reg.def = c
			return
		}
		reg.named[name] = c
	}
	if hasDefault {
		build("", host, user, pass, providerTimeout, providerAllow)
	}
	for _, name := range sortedKeys(entries) {
		e := entries[name]
		allow := providerAllow
		if !e.AllowUnverifiedFirmware.IsNull() {
			allow = e.AllowUnverifiedFirmware.ValueBool()
		}
		build(name, e.Host.ValueString(), e.Username.ValueString(), e.Password.ValueString(), timeoutOr(e.TimeoutSeconds, providerTimeout), allow)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.ResourceData = reg
	resp.DataSourceData = reg
}

// sortedKeys returns the map's keys in a stable order, so diagnostics and
// client creation do not depend on map iteration order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Resources lists the resources the provider manages.
func (p *tplinkProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{NewLEDResource, NewIGMPSnoopingResource, NewLoopPreventionResource, NewPortResource, NewQoSResource, NewBandwidthLimitResource, NewStormLimitResource, NewPortMirrorResource, NewLAGResource, NewVLANPortBasedGroupResource, NewSystemResource, NewVLANModeResource, NewVLAN8021QGroupResource, NewVLAN8021QPVIDResource, NewManagementIPResource}
}

// DataSources lists the read-only data sources the provider offers.
func (p *tplinkProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewSystemInfoDataSource, NewPortsDataSource, NewPortStatisticsDataSource, NewLAGsDataSource, NewPortMirrorDataSource, NewVLANMTUDataSource, NewVLANPortBasedDataSource, NewVLAN8021QDataSource, NewQoSDataSource, NewBandwidthControlDataSource, NewStormControlDataSource, NewConfigBackupDataSource}
}
