// Package lockwait holds the shapes the lockwait check is built to find and to leave alone. The historic
// one is written by hand after escp-go#154: a firmware gate shared by several clients held its lock while
// it read the firmware version from the switch, so a client whose switch did not answer kept every other
// client from writing.
package lockwait

import (
	"context"
	"sync"
)

// Reader is what a gate reads through: a call on it goes to the switch and waits for the reply.
type Reader interface {
	Info(ctx context.Context) (string, error)
}

// firmwareGate remembers the builds found verified.
type firmwareGate struct {
	mu       sync.Mutex
	verified map[string]bool
}

// check is #154: the lock is held across the read of the switch.
func (g *firmwareGate) check(ctx context.Context, sw Reader, key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.verified[key] {
		return nil
	}
	fw, err := sw.Info(ctx) // want `call of sw.Info, which takes a context while g.mu is held \(locked at line 25\)`
	if err != nil {
		return err
	}
	g.verified[key] = fw != ""
	return nil
}
