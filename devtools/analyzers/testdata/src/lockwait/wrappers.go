package lockwait

import (
	"context"
	"sync"
)

// client has lock wrappers that return with the lock held, as the library's client has: they are named
// to the check with -lockwait.lockfuncs, and their receiver, or the &argument, is the lock.
type client struct {
	mu     sync.Mutex
	modify sync.Mutex
	ch     chan int
}

func (c *client) lock(ctx context.Context) error { return nil }

func (c *client) beginModify(ctx context.Context) (func(), error) { return func() {}, nil }

func acquire(ctx context.Context, mu *sync.Mutex) error { return nil }

func (c *client) wrapped(ctx context.Context) error {
	if err := c.lock(ctx); err != nil {
		return err
	}
	work(ctx) // want `call of work, which takes a context while c.mu is held \(locked at line 23\)`
	return nil
}

func (c *client) viaFunction(ctx context.Context) error {
	if err := acquire(ctx, &c.modify); err != nil {
		return err
	}
	c.ch <- 1 // want `channel send while c.modify is held`
	c.modify.Unlock()
	c.ch <- 1
	return nil
}

func (c *client) modifyThenWork(ctx context.Context) error {
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	work(ctx) // want `call of work, which takes a context while c.modify is held`
	return nil
}

// nested holds two locks: the reason on the inner one does not explain the outer one.
func (c *client) nested(ctx context.Context) error {
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	c.mu.Lock() // lockwait: the exchange is one at a time
	defer c.mu.Unlock()
	work(ctx) // want `call of work, which takes a context while c.modify is held`
	return nil
}

// nestedBoth explains both.
func (c *client) nestedBoth(ctx context.Context) error {
	// lockwait: the guard is held across the read and the write
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	c.mu.Lock() // lockwait: the exchange is one at a time
	defer c.mu.Unlock()
	work(ctx)
	return nil
}

// keepsModify takes and releases c.mu inside the modify section: the guard stays held (SetIPConfig of the
// library has this shape), and the release of the other lock does not end it.
func (c *client) keepsModify(ctx context.Context) error {
	end, err := c.beginModify(ctx)
	if err != nil {
		return err
	}
	defer end()
	c.mu.Lock()
	c.mu.Unlock()
	work(ctx) // want `call of work, which takes a context while c.modify is held`
	return nil
}
