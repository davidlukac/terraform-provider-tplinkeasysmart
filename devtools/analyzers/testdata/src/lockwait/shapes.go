package lockwait

import (
	"context"
	"net"
	"sync"
	"time"
)

// Conn has the shape of the library's socket interface: reads and writes with a deadline.
type Conn interface {
	Read(b []byte) (int, error)
	Write(b []byte) (int, error)
	SetReadDeadline(t time.Time) error
}

type box struct {
	mu   sync.Mutex
	rw   sync.RWMutex
	ch   chan int
	wg   sync.WaitGroup
	conn Conn
}

func work(ctx context.Context) {}

func (b *box) sleeps() {
	b.mu.Lock()
	time.Sleep(time.Second) // want `time.Sleep while b.mu is held`
	b.mu.Unlock()
}

func (b *box) everything(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	work(ctx)   // want `call of work, which takes a context while b.mu is held`
	b.ch <- 1   // want `channel send while b.mu is held`
	<-b.ch      // want `channel receive while b.mu is held`
	b.wg.Wait() // want `sync.WaitGroup.Wait while b.mu is held`
	buf := make([]byte, 1)
	_, _ = b.conn.Read(buf)  // want `network Read while b.mu is held`
	_, _ = b.conn.Write(buf) // want `network Write while b.mu is held`
	for range b.ch {         // want `range over a channel while b.mu is held`
	}
	select { // want `select without default while b.mu is held`
	case <-b.ch:
	}
	_, _ = net.Dial("udp", "192.0.2.1:9") // want `network or process call net.Dial while b.mu is held`
}

func (b *box) readLock(ctx context.Context) {
	b.rw.RLock()
	defer b.rw.RUnlock()
	work(ctx) // want `call of work, which takes a context while b.rw is held`
}

func (b *box) released(ctx context.Context) {
	b.mu.Lock()
	b.mu.Unlock()
	work(ctx)
	b.ch <- 1
}

func (b *box) deferredClosure(ctx context.Context) {
	b.mu.Lock()
	defer func() { b.mu.Unlock() }()
	work(ctx) // want `call of work, which takes a context while b.mu is held`
}

func (b *box) selectDefault() {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case b.ch <- 1:
	default:
	}
}

func (b *box) earlyReturn(ctx context.Context, bad bool) {
	b.mu.Lock()
	if bad {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	work(ctx)
}

func (b *box) branches(ctx context.Context, which int) {
	b.mu.Lock()
	switch which {
	case 0:
		b.mu.Unlock()
	default:
	}
	work(ctx) // want `call of work, which takes a context while b.mu is held`
	b.mu.Unlock()
}

func (b *box) loop(ctx context.Context) {
	for i := 0; i < 2; i++ {
		b.mu.Lock()
		work(ctx) // want `call of work, which takes a context while b.mu is held`
		b.mu.Unlock()
	}
	work(ctx)
}

// closure: a function literal is a function of its own, the lock of the enclosing one is not held in it.
func (b *box) closure(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	go func() { work(ctx) }()
	f := func() {
		b.mu.Lock()
		work(ctx) // want `call of work, which takes a context while b.mu is held`
		b.mu.Unlock()
	}
	_ = f
}

func (b *box) noLock(ctx context.Context) {
	work(ctx)
	b.ch <- 1
	time.Sleep(time.Second)
}

func (b *box) conversion() {
	b.mu.Lock()
	defer b.mu.Unlock()
	_ = time.Duration(3)
	_ = len(b.ch)
}

// whole explains the whole function. lockwait: the lock is the serialization of the exchange.
func (b *box) whole(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	work(ctx)
}

func (b *box) noReason(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	/* lockwait: */ work(ctx) // want `a lockwait annotation needs a reason`
}

// A concrete type with the deadline method counts as a connection too, a type without it does not.
type udp struct{}

func (*udp) Read([]byte) (int, error)        { return 0, nil }
func (*udp) SetReadDeadline(time.Time) error { return nil }

type buffer struct{}

func (buffer) Read([]byte) (int, error) { return 0, nil }

func (b *box) concrete(u *udp, buf buffer) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, _ = u.Read(nil) // want `network Read while b.mu is held`
	_, _ = buf.Read(nil)
}
