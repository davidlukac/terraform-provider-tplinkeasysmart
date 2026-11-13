package validatecall

import (
	"context"
	"errors"
)

// SetFixed validates the value first.
func (c *Client) SetFixed(ctx context.Context, p Port) error {
	if err := p.Validate(); err != nil {
		return err
	}
	return c.send(ctx, p)
}

// SetEach validates each element of the list, and refuses an empty one (len and a conversion are no use).
func (c *Client) SetEach(ctx context.Context, ports []Port) error {
	if len(ports) == 0 {
		return errors.New("no ports")
	}
	for _, p := range ports {
		if err := p.Validate(); err != nil {
			return err
		}
	}
	return c.send(ctx, ports)
}

// SetByIndex validates through an index.
func (c *Client) SetByIndex(ctx context.Context, ports []Port) error {
	for i := range ports {
		if err := ports[i].Validate(); err != nil {
			return err
		}
	}
	return c.send(ctx, ports)
}

// SetPointer validates through a pointer receiver.
func (c *Client) SetPointer(ctx context.Context, l *Limit) error {
	if err := l.Validate(); err != nil {
		return err
	}
	return c.send(ctx, l)
}

// SetThroughHelper validates through a helper of the package, which validates through another.
func (c *Client) SetThroughHelper(ctx context.Context, p Port) error {
	if err := requirePort(p); err != nil {
		return err
	}
	return c.send(ctx, p)
}

func requirePort(p Port) error { return checkPort(p) }

func checkPort(p Port) error { return p.Validate() }

// SetThroughMethod validates through a method of the client that does.
func (c *Client) SetThroughMethod(ctx context.Context, p Port) error {
	if err := c.require(p); err != nil {
		return err
	}
	return c.send(ctx, p)
}

func (c *Client) require(p Port) error { return p.Validate() }

// SetThroughField validates a value taken from the parameter.
func (c *Client) SetThroughField(ctx context.Context, s Setting) error {
	port := s.Port
	if err := port.Validate(); err != nil {
		return err
	}
	return c.send(ctx, s)
}

// SetInClosure validates inside a closure of the function.
func (c *Client) SetInClosure(ctx context.Context, p Port) error {
	check := func() error { return p.Validate() }
	if err := check(); err != nil {
		return err
	}
	return c.send(ctx, p)
}

// SetNothing takes a value with no rule of its own, and an interface: nothing to call.
func (c *Client) SetNothing(ctx context.Context, s Setting, r Reader) error {
	return c.send(ctx, []any{s, r})
}

// SetNotAnArgument does not take a value, only the context: the receiver is not a parameter.
func (p Port) SetNotAnArgument(ctx context.Context) error { return nil }

// ValidateMore is a validator itself: it does not need to call another.
func ValidateMore(p Port) error { return nil }

func unexported(p Port) error { return c(p) }
