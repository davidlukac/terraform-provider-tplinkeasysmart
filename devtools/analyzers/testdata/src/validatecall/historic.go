package validatecall

import "context"

// SetPriorities is escp-go#169: the list is handed to the write as it is, and nothing applies the rule
// of its elements.
func (c *Client) SetPriorities(ctx context.Context, ports []Port) error { // want `SetPriorities takes ports \(Port has a Validate method\) and never validates it`
	return c.send(ctx, ports)
}

// SetOne is the same with one value.
func (c *Client) SetOne(ctx context.Context, p Port) error { // want `SetOne takes p \(Port has a Validate method\) and never validates it`
	return c.send(ctx, p)
}

// SetLimit takes a pointer: the rule is on the pointer receiver and is found all the same.
func (c *Client) SetLimit(ctx context.Context, l *Limit) error { // want `SetLimit takes l \(Limit has a Validate method\) and never validates it`
	return c.send(ctx, l)
}

// SetMany takes a variadic list.
func SetMany(ports ...Port) error { // want `SetMany takes ports \(Port has a Validate method\) and never validates it`
	return c(ports[0])
}

// Late validates after the value was already sent.
func (c *Client) Late(ctx context.Context, p Port) error {
	if err := c.send(ctx, p); err != nil { // want `\(\*Client\)\.Late uses p \(Port has a Validate method\) before it is validated \(line \d+\)`
		return err
	}
	return p.Validate()
}

// TwoValues validates one of its two.
func (c *Client) TwoValues(ctx context.Context, a, b Port) error { // want `TwoValues takes b \(Port has a Validate method\) and never validates it`
	if err := a.Validate(); err != nil {
		return err
	}
	return c.send(ctx, []Port{a, b})
}

// Unnamed variable: a helper that does not look at the parameter does not count.
func (c *Client) OtherHelper(ctx context.Context, p Port, q Port) error { // want `OtherHelper takes p \(Port has a Validate method\) and never validates it`
	if err := validateOther(q); err != nil {
		return err
	}
	return c.send(ctx, p)
}

func validateOther(q Port) error { return q.Validate() }

// A closure that validates is part of the function.
func (c *Client) InClosure(ctx context.Context, p Port) error { // want `InClosure takes p \(Port has a Validate method\) and never validates it`
	_ = func() error { return nil }
	return c.send(ctx, p)
}
