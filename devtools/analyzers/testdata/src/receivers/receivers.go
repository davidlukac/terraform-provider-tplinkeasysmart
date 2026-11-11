// Package receivers is checked with the receiver filter set to the client's type: the client's own
// read-then-write is reported, the same names on another type are not.
package receivers

import "context"

type Client struct{}

func (Client) Info(context.Context) int { return 0 }

func (Client) SetThing(context.Context, int) {}

func (c Client) Bad(ctx context.Context) { // want `Bad reads`
	_ = c.Info(ctx)
	c.SetThing(ctx, 1)
}

type Config struct{}

func (Config) Info() int { return 0 }

func (Config) SetThing(int) {}

func Other(c Config) {
	_ = c.Info()
	c.SetThing(1)
}

func Local(c *Client, d Config) {
	_ = c
	_ = d.Info()
	d.SetThing(1)
}
