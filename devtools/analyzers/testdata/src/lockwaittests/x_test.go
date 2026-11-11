package lockwaittests

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
	work(context.Background()) // want `call of work, which takes a context while x.mu is held`
	x.mu.Unlock()
	t.Log("done")
}
