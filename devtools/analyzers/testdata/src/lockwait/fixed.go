package lockwait

import "context"

// checkFixed reads outside the lock and takes it only to look at and to record the answer, which is the fix of #154.
func (g *firmwareGate) checkFixed(ctx context.Context, sw Reader, key string) error {
	g.mu.Lock()
	known := g.verified[key]
	g.mu.Unlock()
	if known {
		return nil
	}
	fw, err := sw.Info(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.verified[key] = fw != ""
	g.mu.Unlock()
	return nil
}

// checkNoted keeps the read under the lock and says what ends the wait. lockwait: the gate is private to one client, whose Close ends the read.
func (g *firmwareGate) checkNoted(ctx context.Context, sw Reader, key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, err := sw.Info(ctx)
	return err
}

// checkLine explains the whole critical section on the line of the Lock.
func (g *firmwareGate) checkLine(ctx context.Context, sw Reader) error {
	g.mu.Lock() // lockwait: the context ends the read, and nothing else uses the gate
	defer g.mu.Unlock()
	_, err := sw.Info(ctx)
	return err
}

// checkOperation explains the single operation, on the line above it.
func (g *firmwareGate) checkOperation(ctx context.Context, sw Reader) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	// lockwait: the context ends the read
	_, err := sw.Info(ctx)
	return err
}
