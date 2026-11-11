// Package nowrappers is checked with no lock wrapper named: the calls that would take a lock for the
// caller are plain calls, and the plain mutex is still seen.
package nowrappers

import (
	"context"
	"sync"
)

type client struct{ mu sync.Mutex }

func (c *client) lock(ctx context.Context) error { return nil }

func work(ctx context.Context) {}

func (c *client) wrapped(ctx context.Context) {
	_ = c.lock(ctx)
	work(ctx)
}

func (c *client) plain(ctx context.Context) {
	c.mu.Lock()
	work(ctx) // want `call of work, which takes a context while c.mu is held`
	c.mu.Unlock()
}
