package provider

import (
	"context"
	"sync"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// SwitchClient is the seam between the Terraform resources and the switch
// transport. *escp.Client implements it; tests substitute a fake.
type SwitchClient interface {
	Info(ctx context.Context) (escp.Info, error)
	GetLED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
}

// lazyClient dials the switch on first use so that `terraform validate` and
// plans that touch no switch resources never bind the UDP port.
type lazyClient struct {
	cfg  escp.Config
	once sync.Once
	c    *escp.Client
	err  error
}

func (l *lazyClient) get() (*escp.Client, error) {
	l.once.Do(func() { l.c, l.err = escp.Dial(l.cfg) })
	return l.c, l.err
}

func (l *lazyClient) Info(ctx context.Context) (escp.Info, error) {
	c, err := l.get()
	if err != nil {
		return escp.Info{}, err
	}
	return c.Info(ctx)
}

func (l *lazyClient) GetLED(ctx context.Context) (bool, error) {
	c, err := l.get()
	if err != nil {
		return false, err
	}
	return c.GetLED(ctx)
}

func (l *lazyClient) SetLED(ctx context.Context, on bool) error {
	c, err := l.get()
	if err != nil {
		return err
	}
	return c.SetLED(ctx, on)
}
