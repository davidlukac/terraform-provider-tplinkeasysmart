package provider

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/safety"
)

// Environment variables that back the provider arguments.
const (
	envHost     = "TPLINK_EASYSMART_HOST"
	envMAC      = "TPLINK_EASYSMART_MAC"
	envUsername = "TPLINK_EASYSMART_USERNAME"
	envPassword = "TPLINK_EASYSMART_PASSWORD" //nolint:gosec // the name of an environment variable, not a credential
	// envAllowUnverified backs allow_unverified_firmware for the provider's default; a switch entry still overrides it.
	envAllowUnverified = "TPLINK_EASYSMART_ALLOW_UNVERIFIED_FIRMWARE"
)

var (
	_ provider.Provider                   = &tplinkProvider{}
	_ provider.ProviderWithValidateConfig = &tplinkProvider{}
)

type tplinkProvider struct {
	version string
	// newClient builds the transport; tests replace it with a fake.
	newClient func(escp.Config) (SwitchClient, error)
}

// New returns the provider factory used by main.
func New(version string) func() provider.Provider {
	return NewWithDialer(version, func(c escp.Config) (SwitchClient, error) { return escp.Dial(c) })
}

// NewWithDialer is New with the transport chosen by the caller: dial receives the configuration
// the provider built for each switch (including its write gate) and returns the client the
// resources use. The regression suite uses it to aim the real provider at a fake switch on a
// loopback port; the provider has no argument for that, on purpose.
func NewWithDialer(version string, dial func(escp.Config) (SwitchClient, error)) func() provider.Provider {
	return func() provider.Provider {
		return &tplinkProvider{version: version, newClient: dial}
	}
}

type providerModel struct {
	Host           types.String `tfsdk:"host"`
	MAC            types.String `tfsdk:"mac"`
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
	MAC                     types.String `tfsdk:"mac"`
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
				Description: "IPv4 address or hostname of the switch. Falls back to TPLINK_EASYSMART_HOST. Optional when `mac` is set.",
			},
			"mac": schema.StringAttribute{
				Optional:    true,
				Validators:  []validator.String{validMAC()},
				Description: macArgDescription + " Falls back to TPLINK_EASYSMART_MAC.",
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
					"Falls back to TPLINK_EASYSMART_ALLOW_UNVERIFIED_FIRMWARE (`true` or `false`). Can be overridden per switch in the `switches` map.",
			},
			"switches": schema.MapNestedAttribute{
				Optional: true,
				Description: "Switches managed by this provider, by name. Select one on a resource or data source with `switch = \"<name>\"`. " +
					"May be combined with `host`, which then names the default switch used when `switch` is omitted.",
				Validators: []validator.Map{mapvalidator.KeysAre(validSwitchName()...)},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"host":     schema.StringAttribute{Optional: true, Description: "IPv4 address or hostname of the switch. Optional when `mac` is set; at least one of the two is required."},
					"mac":      schema.StringAttribute{Optional: true, Validators: []validator.String{validMAC()}, Description: macArgDescription},
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

// macArgDescription is shared by the provider block and the `switches` entries.
const macArgDescription = "MAC address of the switch (`aa:bb:cc:dd:ee:ff`, colons or hyphens, any case; `system_info` reports it as `mac_address`). " +
	"It pins the identity: a device with another MAC at `host` is never written to. Without `host`, or when `host` does not answer, " +
	"the switch is found by a layer-2 discovery on the first request, so its address may change (for example under DHCP). " +
	"Discovery needs the same layer-2 segment; use `host` as well when Terraform runs elsewhere."

// ValidateConfig requires every `switches` entry to name its switch by `host`, `mac` or both.
// The provider's own switch is checked in Configure, because its values may come from the environment.
func (p *tplinkProvider) ValidateConfig(ctx context.Context, req provider.ValidateConfigRequest, resp *provider.ValidateConfigResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Switches.IsNull() || cfg.Switches.IsUnknown() {
		return
	}
	var entries map[string]switchModel
	resp.Diagnostics.Append(cfg.Switches.ElementsAs(ctx, &entries, false)...)
	for _, name := range sortedKeys(entries) {
		e := entries[name]
		if e.Host.IsNull() && e.MAC.IsNull() {
			resp.Diagnostics.AddAttributeError(pathRoot("switches").AtMapKey(name), "Missing switch host or MAC address",
				"Switch "+name+" needs `host`, `mac` or both: the MAC finds the switch and pins its identity, the host says where to look first.")
		}
	}
}

// verifiedBuilds lists the verified firmware builds for the attribute description.
func verifiedBuilds() string {
	builds := safety.VerifiedFirmwareBuilds()
	names := make([]string, len(builds))
	for i, b := range builds {
		names[i] = b.String()
	}
	return strings.Join(names, ", ")
}

// defaultTimeout is the per-request timeout when none is configured.
const defaultTimeout = 3 * time.Second

func timeoutOr(configured types.Int64, fallback time.Duration) time.Duration {
	if !configured.IsNull() && configured.ValueInt64() > 0 {
		return time.Duration(configured.ValueInt64()) * time.Second
	}
	return fallback
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
