package provider

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &systemInfoDataSource{}
	_ datasource.DataSourceWithConfigure = &systemInfoDataSource{}
)

// NewSystemInfoDataSource returns the tplinkeasysmart_system_info data source.
func NewSystemInfoDataSource() datasource.DataSource { return &systemInfoDataSource{} }

type systemInfoDataSource struct {
	src    switchSource
	client systemInfoReader // set on the per-call copy made by bound()
	sw     types.String     // the switch that copy is bound to, for ids
}

// systemInfoReader is the part of the switch API this data source reads.
type systemInfoReader interface {
	Info(ctx context.Context) (escp.Info, error)
	System(ctx context.Context) (escp.System, error)
}

// bound returns a copy of the data source wired to the client of the named switch.
func (d *systemInfoDataSource) bound(add func(string, string), name types.String) *systemInfoDataSource {
	return bindSwitch(add, d.src, name, d, func(b *systemInfoDataSource, c systemInfoReader, sw types.String) { b.client, b.sw = c, sw })
}

type systemInfoModel struct {
	Switch   types.String `tfsdk:"switch"`
	ID       types.String `tfsdk:"id"`
	Model    types.String `tfsdk:"model"`
	Hostname types.String `tfsdk:"hostname"`
	MAC      types.String `tfsdk:"mac_address"`
	Firmware types.String `tfsdk:"firmware_version"`
	Hardware types.String `tfsdk:"hardware_version"`
	IP       types.String `tfsdk:"ip_address"`
	Netmask  types.String `tfsdk:"netmask"`
	Gateway  types.String `tfsdk:"gateway"`
	DHCP     types.Bool   `tfsdk:"dhcp"`
	Factory  types.Bool   `tfsdk:"factory_default"`
	Ports    types.Int64  `tfsdk:"port_count"`
	AutoSave types.Bool   `tfsdk:"auto_save"`
}

func (d *systemInfoDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_info"
}

func (d *systemInfoDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	str := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, Description: desc}
	}
	resp.Schema = schema.Schema{
		Description: "Identity and network settings of the switch, read through unauthenticated discovery. " +
			"Also serves as a connectivity check.",
		Attributes: map[string]schema.Attribute{
			attrSwitch:         dataSourceSwitchAttribute(),
			"id":               str("The switch MAC address."),
			"model":            str("Model, e.g. TL-SG108E."),
			"hostname":         str("Device description."),
			"mac_address":      str("MAC address."),
			"firmware_version": str("Firmware version string."),
			"hardware_version": str("Hardware version string."),
			"ip_address":       str("Management IP address."),
			"netmask":          str("Management subnet mask."),
			"gateway":          str("Default gateway."),
			"dhcp":             schema.BoolAttribute{Computed: true, Description: "Whether the management IP comes from DHCP."},
			"factory_default":  schema.BoolAttribute{Computed: true, Description: "The switch reports it has never been configured (factory credentials still apply)."},
			"port_count":       schema.Int64Attribute{Computed: true, Description: "Number of physical ports."},
			"auto_save":        schema.BoolAttribute{Computed: true, Description: "Whether configuration changes are saved to flash automatically."},
		},
	}
}

func (d *systemInfoDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.src = sourceFrom(req.ProviderData, &resp.Diagnostics)
}

// Read binds the data source to the switch selected in the configuration, then reads it.
func (d *systemInfoDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	sw := switchOf(ctx, req.Config, &resp.Diagnostics)
	if b := d.bound(resp.Diagnostics.AddError, sw); b != nil {
		b.readBound(ctx, req, resp)
		keepSwitch(ctx, sw, &resp.State, &resp.Diagnostics)
	}
}

func (d *systemInfoDataSource) readBound(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.Info(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading switch info failed", err.Error())
		return
	}
	sys, err := d.client.System(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading switch system settings failed", err.Error())
		return
	}
	state := systemInfoModel{
		ID: types.StringValue(info.MAC.String()), Model: types.StringValue(info.Model), Hostname: types.StringValue(info.Hostname),
		MAC: types.StringValue(info.MAC.String()), Firmware: types.StringValue(info.Firmware), Hardware: types.StringValue(info.Hardware),
		IP: types.StringValue(addrString(info.IP)), Netmask: types.StringValue(addrString(info.Netmask)), Gateway: types.StringValue(addrString(info.Gateway)),
		DHCP: types.BoolValue(info.DHCP), Factory: types.BoolValue(info.FactoryDefault),
		Ports: types.Int64Value(int64(sys.PortCount)), AutoSave: types.BoolValue(sys.AutoSave),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
