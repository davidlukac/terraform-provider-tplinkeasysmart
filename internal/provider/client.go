package provider

import (
	"context"

	"github.com/davidlukac/escp-go/model"
)

// SwitchClient is the union of everything the provider asks of the switch, as
// implemented by *escp.Client (and by test fakes). Resources and data sources
// do not depend on it directly: each declares the small interface it needs.
type SwitchClient interface {
	Info(ctx context.Context) (model.Info, error)
	LED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
	Ports(ctx context.Context) ([]model.Port, error)
	SetPort(ctx context.Context, cfg model.PortConfig) error
	PortStats(ctx context.Context) ([]model.PortStats, error)
	LAGs(ctx context.Context) ([]model.LAG, error)
	SetLAG(ctx context.Context, lag model.LAG) error
	IGMP(ctx context.Context) (model.IGMP, error)
	SetIGMP(ctx context.Context, cfg model.IGMPConfig) error
	LoopPrevention(ctx context.Context) (bool, error)
	SetLoopPrevention(ctx context.Context, on bool) error
	PortMirror(ctx context.Context) (model.Mirror, error)
	SetPortMirror(ctx context.Context, m model.Mirror) error
	MTUVLAN(ctx context.Context) (model.MTUVLAN, error)
	PortBasedVLAN(ctx context.Context) (model.PortBasedVLAN, error)
	SetPortBasedVLAN(ctx context.Context, group model.PortVLAN) error
	Dot1Q(ctx context.Context) (model.Dot1Q, error)
	VLANMode(ctx context.Context) (model.VLANModeSetting, error)
	SetVLANMode(ctx context.Context, s model.VLANModeSetting) error
	SetDot1QVLAN(ctx context.Context, v model.Dot1QVLAN) error
	DeleteDot1QVLAN(ctx context.Context, id model.VLANID) error
	SetPVIDs(ctx context.Context, pvids []model.PortPVID) error
	QoS(ctx context.Context) (model.QoS, error)
	SetQoSMode(ctx context.Context, mode model.QoSMode) error
	SetPortPriorities(ctx context.Context, changes []model.PortPriority) error
	Bandwidth(ctx context.Context) ([]model.PortBandwidth, error)
	SetBandwidth(ctx context.Context, limit model.PortBandwidth) error
	StormControl(ctx context.Context) ([]model.PortStorm, error)
	SetStormControl(ctx context.Context, cfg model.StormConfig) error
	System(ctx context.Context) (model.System, error)
	SetHostname(ctx context.Context, name model.Hostname) error
	SetIPConfig(ctx context.Context, cfg model.IPConfig) error
	EnableDHCP(ctx context.Context) error
	ConfigBackup(ctx context.Context) (model.ConfigFile, error)
	// Close ends the client's claim on its switch and releases its socket: Terraform configures the
	// provider more than once in a process, and the library refuses a second client of a switch
	// that the first has not given up.
	Close() error
	// SwitchMAC is the MAC address of the switch, which the firmware gate remembers its verdict by;
	// zero while it is not known.
	SwitchMAC() model.MAC
}
