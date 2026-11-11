// Package readwrite holds the shapes the readwrite check is built to find and to leave alone. The
// historic ones are written by hand after the defects of escp-go#142 (a method reads the settings, changes
// one field and writes them all back, while another call does the same) and #184 (a limit guard reads the
// other setting, then the write follows outside the critical section).
package readwrite

import (
	"context"
	"sync"
)

// Client has the shape of the library's client: readers, writers, and the modify guard.
type Client struct{ modify sync.Mutex }

// Info reads the system settings.
func (c *Client) Info(context.Context) (int, error) { return 0, nil }

// StormControl reads the storm control rows.
func (c *Client) StormControl(context.Context) ([]int, error) { return nil, nil }

func (c *Client) requireNoStorm(ctx context.Context) error {
	_, err := c.StormControl(ctx)
	return err
}

func (c *Client) setTLVs(context.Context, int) error { return nil }

func (c *Client) writeSystem(context.Context, int) error { return nil }

func (c *Client) beginModify(context.Context) (func(), error) { return func() {}, nil }

// SetHostname is escp-go#142: the current settings are read, the hostname replaced, and everything
// written back, with nothing stopping a concurrent call from changing the address in between.
func (c *Client) SetHostname(ctx context.Context, name int) error { // want `SetHostname reads \(line \d+\) and then writes \(line \d+\) without holding the modify guard`
	if name < 0 {
		return nil
	}
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	return c.writeSystem(ctx, info+name)
}

// SetBandwidth is escp-go#184: the guard reads the other setting, then the write follows.
func (c *Client) SetBandwidth(ctx context.Context, limit int) error { // want `SetBandwidth reads \(line \d+\) and then writes \(line \d+\) without holding the modify guard`
	if err := c.requireNoStorm(ctx); err != nil {
		return err
	}
	return c.setTLVs(ctx, limit)
}

// SetLate takes the guard after the read, which is the same defect: the value read is already stale.
func (c *Client) SetLate(ctx context.Context, v int) error { // want `SetLate reads \(line \d+\) and then writes \(line \d+\) without holding the modify guard`
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	return c.setTLVs(ctx, info+v)
}
