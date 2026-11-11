package readwrite

import "context"

// SetHostnameFixed holds the guard across the read and the write.
func (c *Client) SetHostnameFixed(ctx context.Context, name int) error {
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	return c.writeSystem(ctx, info+name)
}

// SetBandwidthFixed takes the guard first, and the check inside it.
func (c *Client) SetBandwidthFixed(ctx context.Context, limit int) error {
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	if err := c.requireNoStorm(ctx); err != nil {
		return err
	}
	return c.setTLVs(ctx, limit)
}

// SetLocked takes the guard as a mutex field.
func (c *Client) SetLocked(ctx context.Context, v int) error {
	c.modify.Lock()
	defer c.modify.Unlock()
	info, _ := c.Info(ctx)
	return c.setTLVs(ctx, info+v)
}

// SetInClosure runs both inside a function handed to the guard.
func (c *Client) SetInClosure(ctx context.Context, v int) error {
	return c.withModify(func() error {
		info, _ := c.Info(ctx)
		return c.setTLVs(ctx, info+v)
	})
}

func (c *Client) withModify(f func() error) error { return f() }

// SetOnly writes without reading.
func (c *Client) SetOnly(ctx context.Context, v int) error { return c.setTLVs(ctx, v) }

// ReadOnly reads without writing.
func (c *Client) ReadOnly(ctx context.Context) (int, error) { return c.Info(ctx) }

// SetThenRead writes, then reads back to verify: the read does not feed the write.
func (c *Client) SetThenRead(ctx context.Context, v int) error {
	if err := c.setTLVs(ctx, v); err != nil {
		return err
	}
	_, err := c.Info(ctx)
	return err
}

// SetNoted reads and writes without the guard, and says why. readwrite: the read only warms a cache, the write is absolute.
func (c *Client) SetNoted(ctx context.Context, v int) error {
	_, _ = c.Info(ctx)
	return c.setTLVs(ctx, v)
}

// plain is a helper function, not a method: a plain function is not a step.
func plain(ctx context.Context, c *Client) error {
	_, _ = Info(ctx)
	return c.setTLVs(ctx, 1)
}

// Info of the package is a function and not a read of the client.
func Info(context.Context) (int, error) { return 0, nil }
