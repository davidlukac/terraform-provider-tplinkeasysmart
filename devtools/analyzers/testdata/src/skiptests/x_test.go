package skiptests

import (
	"context"
	"sync"
	"testing"
)

type c struct{ mu sync.Mutex }

func work(ctx context.Context) {}

func (x *c) Info(context.Context) int { return 0 }

func (x *c) SetThing(context.Context) {}

func TestSomething(t *testing.T) {
	var x c
	x.mu.Lock()
	work(context.Background())
	x.mu.Unlock()
	t.Log("done")
}

func (x *c) Both(ctx context.Context) {
	_ = x.Info(ctx)
	x.SetThing(ctx)
}
