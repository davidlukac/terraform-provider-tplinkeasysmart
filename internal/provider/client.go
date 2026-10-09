package provider

import (
	"context"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// SwitchClient is the seam between the Terraform resources and the switch
// transport. *escp.Client implements it; tests substitute a fake.
type SwitchClient interface {
	Info(ctx context.Context) (escp.Info, error)
	GetLED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
	Ports(ctx context.Context) ([]escp.Port, error)
	PortStatistics(ctx context.Context) ([]escp.PortStats, error)
	LAGs(ctx context.Context) ([]escp.LAG, error)
	IGMP(ctx context.Context) (escp.IGMP, error)
	SetIGMP(ctx context.Context, enabled, suppression bool) error
	LoopPrevention(ctx context.Context) (bool, error)
	SetLoopPrevention(ctx context.Context, on bool) error
	PortMirror(ctx context.Context) (escp.Mirror, error)
	MTUVLAN(ctx context.Context) (escp.MTUVLAN, error)
	PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error)
	VLAN8021Q(ctx context.Context) (escp.Dot1Q, error)
	QoS(ctx context.Context) (escp.QoS, error)
	Bandwidth(ctx context.Context) ([]escp.PortBandwidth, error)
}
