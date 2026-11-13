// Package validatecall holds the shapes the validatecall check is built to find and to leave alone. The
// historic one is written by hand after escp-go#169: a writer takes a list of values, each with its own
// rule, and sends them without applying it (an empty list became a full rewrite).
package validatecall

import (
	"context"
	"errors"
	"fmt"
)

// Port is a value with its own rule.
type Port int

// Validate refuses a port outside 1-32.
func (p Port) Validate() error {
	if p < 1 || p > 32 {
		return errors.New("invalid port")
	}
	return nil
}

// Limit is a record with a rule that is applied through a pointer receiver.
type Limit struct{ Port Port }

// Validate refuses a limit on an invalid port.
func (l *Limit) Validate() error { return l.Port.Validate() }

// Setting has no rule of its own, so a function that takes one has nothing to call.
type Setting struct{ Port Port }

// Reader is an interface; what it holds is not known, so it is not looked at.
type Reader interface{ Validate() error }

// Client has the shape of the library's client.
type Client struct{}

func (c *Client) send(ctx context.Context, v any) error { return nil }

// hidden is an unexported type, so the exported-looking method of it is not part of the API.
type hidden struct{}

// Take is not part of the exported API.
func (hidden) Take(p Port) error { return c(p) }

func c(p Port) error { return fmt.Errorf("%d", p) }
