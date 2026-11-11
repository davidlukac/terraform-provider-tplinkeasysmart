package lockwait

import (
	"context"
	"net/http"
	"os/exec"
)

// bothBranchesUnlock: the lock is released on every path, nothing follows under it.
func (b *box) bothBranchesUnlock(ctx context.Context, x bool) {
	b.mu.Lock()
	if x {
		b.mu.Unlock()
	} else {
		b.mu.Unlock()
	}
	work(ctx)
}

// oneBranchKeeps: the lock may still be held after the if.
func (b *box) oneBranchKeeps(ctx context.Context, x bool) {
	b.mu.Lock()
	if x {
		b.mu.Unlock()
	} else if ctx != nil {
		b.ch = nil
	}
	work(ctx) // want `call of work, which takes a context while b.mu is held`
	b.mu.Unlock()
}

// bothBranchesEnd: both ways out leave the function, so what follows is unreachable and silent.
func (b *box) bothBranchesEnd(ctx context.Context, x bool) {
	b.mu.Lock()
	if x {
		return
	} else {
		panic("x")
	}
}

func (b *box) panics(ctx context.Context) {
	b.mu.Lock()
	panic("stop")
}

func (b *box) kinds(ctx context.Context, v any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch x := v.(type) {
	case int:
		_ = x
		work(ctx) // want `call of work, which takes a context while b.mu is held`
	}
	switch y := 1; y {
	case 1:
		work(ctx) // want `call of work, which takes a context while b.mu is held`
	}
	var z = <-b.ch // want `channel receive while b.mu is held`
	_ = z
	b.wg.Add(1)
	b.ch <- func() int { return 1 }() // want `channel send while b.mu is held`
	b.wg.Done()
	_, _ = http.Get("http://192.0.2.1/") // want `network or process call net/http.Get while b.mu is held`
	_ = exec.Command("true").Run()       // want `os/exec.Command while b.mu is held` `os/exec.Cmd\).Run while b.mu is held`
	b.mu.TryLock()
out:
	for {
		break out
	}
	go work(ctx)
	defer work(ctx)
	b.ch <- 1 // want `channel send while b.mu is held`
}

func (b *box) incdec(n *int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	*n++
	for i := range 2 {
		_ = i
	}
}
