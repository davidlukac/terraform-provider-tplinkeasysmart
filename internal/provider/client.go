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
}
