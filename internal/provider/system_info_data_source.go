package provider

import (
	"context"
	"fmt"

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

type systemInfoDataSource struct{ client SwitchClient }

type systemInfoModel struct {
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
		},
	}
}

func (d *systemInfoDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(SwitchClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("got %T", req.ProviderData))
		return
	}
	d.client = c
}

func (d *systemInfoDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	info, err := d.client.Info(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Reading switch info failed", err.Error())
		return
	}
	state := systemInfoModel{
		ID: types.StringValue(info.MAC), Model: types.StringValue(info.Model), Hostname: types.StringValue(info.Hostname),
		MAC: types.StringValue(info.MAC), Firmware: types.StringValue(info.Firmware), Hardware: types.StringValue(info.Hardware),
		IP: types.StringValue(info.IP), Netmask: types.StringValue(info.Netmask), Gateway: types.StringValue(info.Gateway),
		DHCP: types.BoolValue(info.DHCP),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
