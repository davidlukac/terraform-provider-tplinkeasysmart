package provider

import (
	"context"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// SwitchClient is the union of everything the provider asks of the switch, as
// implemented by *escp.Client (and by test fakes). Resources and data sources
// do not depend on it directly: each declares the small interface it needs.
type SwitchClient interface {
	Info(ctx context.Context) (escp.Info, error)
	GetLED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
	Ports(ctx context.Context) ([]escp.Port, error)
	SetPort(ctx context.Context, id int, enabled bool, speed string, flowControl bool) error
	PortStatistics(ctx context.Context) ([]escp.PortStats, error)
	LAGs(ctx context.Context) ([]escp.LAG, error)
	SetLAG(ctx context.Context, group int, ports []int) error
	IGMP(ctx context.Context) (escp.IGMP, error)
	SetIGMP(ctx context.Context, enabled, suppression bool) error
	LoopPrevention(ctx context.Context) (bool, error)
	SetLoopPrevention(ctx context.Context, on bool) error
	PortMirror(ctx context.Context) (escp.Mirror, error)
	SetPortMirror(ctx context.Context, m escp.Mirror) error
	MTUVLAN(ctx context.Context) (escp.MTUVLAN, error)
	PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error)
	SetPortBasedVLAN(ctx context.Context, id int, ports []int) error
	VLAN8021Q(ctx context.Context) (escp.Dot1Q, error)
	QoS(ctx context.Context) (escp.QoS, error)
	SetQoSMode(ctx context.Context, mode int) error
	SetPortPriorities(ctx context.Context, changes map[int]int) error
	Bandwidth(ctx context.Context) ([]escp.PortBandwidth, error)
	SetBandwidth(ctx context.Context, port, ingressKbps, egressKbps int) error
	StormControl(ctx context.Context) ([]escp.PortStorm, error)
	SetStormControl(ctx context.Context, port int, unknownUnicast, multicast, broadcast bool, rateKbps int) error
	System(ctx context.Context) (escp.System, error)
	SetHostname(ctx context.Context, name string) error
	ConfigBackup(ctx context.Context) ([]byte, error)
}
