package provider

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// SwitchClient is the union of everything the provider asks of the switch, as
// implemented by *escp.Client (and by test fakes). Resources and data sources
// do not depend on it directly: each declares the small interface it needs.
type SwitchClient interface {
	Info(ctx context.Context) (escp.Info, error)
	LED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
	Ports(ctx context.Context) ([]escp.Port, error)
	SetPort(ctx context.Context, cfg escp.PortConfig) error
	PortStatistics(ctx context.Context) ([]escp.PortStats, error)
	LAGs(ctx context.Context) ([]escp.LAG, error)
	SetLAG(ctx context.Context, group int, ports escp.PortSet) error
	IGMP(ctx context.Context) (escp.IGMP, error)
	SetIGMP(ctx context.Context, cfg escp.IGMPConfig) error
	LoopPrevention(ctx context.Context) (bool, error)
	SetLoopPrevention(ctx context.Context, on bool) error
	PortMirror(ctx context.Context) (escp.Mirror, error)
	SetPortMirror(ctx context.Context, m escp.Mirror) error
	MTUVLAN(ctx context.Context) (escp.MTUVLAN, error)
	PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error)
	SetPortBasedVLAN(ctx context.Context, id int, ports escp.PortSet) error
	Dot1Q(ctx context.Context) (escp.Dot1Q, error)
	VLANMode(ctx context.Context) (escp.VLANModeSetting, error)
	SetVLANMode(ctx context.Context, s escp.VLANModeSetting) error
	SetVLAN8021Q(ctx context.Context, v escp.VLAN8021Q) error
	DeleteVLAN8021Q(ctx context.Context, id escp.VLANID) error
	SetPVIDs(ctx context.Context, pvids map[escp.PortID]escp.VLANID) error
	QoS(ctx context.Context) (escp.QoS, error)
	SetQoSMode(ctx context.Context, mode escp.QoSMode) error
	SetPortPriorities(ctx context.Context, changes map[escp.PortID]escp.Priority) error
	Bandwidth(ctx context.Context) ([]escp.PortBandwidth, error)
	SetBandwidth(ctx context.Context, limit escp.PortBandwidth) error
	StormControl(ctx context.Context) ([]escp.PortStorm, error)
	SetStormControl(ctx context.Context, cfg escp.StormConfig) error
	System(ctx context.Context) (escp.System, error)
	SetHostname(ctx context.Context, name string) error
	SetIPConfig(ctx context.Context, cfg escp.IPConfig) error
	EnableDHCP(ctx context.Context) error
	ConfigBackup(ctx context.Context) ([]byte, error)
}
