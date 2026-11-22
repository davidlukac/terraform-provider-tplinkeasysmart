package provider

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
)

// These tests are about requests that are already waiting when the first request of a run fails
// (#222, DC-P06): Terraform runs resources in parallel, the requests of one client are serialized,
// and a latch that decides before a request is queued lets the whole first wave through. They go
// through the production path (registryBuilder.add and Configure, real escp clients over UDP to a
// muted fake, or a client double that the registry wraps like any other), count datagrams and never
// the clock. The only clock in them is the bound that fails a test whose callers never return.

// stuckBound is how long a test waits for callers that should have returned before it fails: the
// bound of a deadlock, never an expected wait.
const stuckBound = 10 * time.Second

// inParallel runs fn(0) ... fn(n-1) at the same moment, waits for all of them and returns what each
// returned. A test whose callers do not return fails after stuckBound.
func inParallel[T any](t *testing.T, n int, fn func(i int) T) []T {
	t.Helper()
	out := make([]T, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			out[i] = fn(i)
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)
	select {
	case <-done:
	case <-time.After(stuckBound):
		t.Fatal("the callers did not return: the admission of the latch is stuck")
	}
	return out
}

// registryOf runs the production path (registryBuilder.add) for each spec with the given
// constructor of raw clients.
func registryOf(t *testing.T, newClient func(escp.Config) (SwitchClient, error), specs ...switchSpec) *switchRegistry {
	t.Helper()
	var diags diag.Diagnostics
	b := &registryBuilder{
		reg:       &switchRegistry{named: map[string]SwitchClient{}},
		diags:     &diags,
		seen:      map[string]string{},
		newClient: newClient,
	}
	for _, s := range specs {
		s.username, s.password = escptest.Username, escptest.Password
		b.add(s)
	}
	if diags.HasError() {
		t.Fatalf("the clients were not built: %v", diags)
	}
	t.Cleanup(func() { _ = b.reg.closeAll() })
	return b.reg
}

// request is one kind of request a test sends.
type request struct {
	name string
	send func(context.Context, SwitchClient) error
}

var (
	readInfo = request{"a read", func(ctx context.Context, c SwitchClient) error { _, err := c.Info(ctx); return err }}
	readLED  = request{"another read", func(ctx context.Context, c SwitchClient) error { _, err := c.LED(ctx); return err }}
	// The configuration read passes the firmware gate, which reads the firmware through the latch and
	// then reads the file through it: two admissions in a row of one call.
	readConfig = request{"the configuration read behind the gate", func(ctx context.Context, c SwitchClient) error {
		_, err := c.ConfigBackup(ctx)
		return err
	}}
)

// datagramsOfOneRequest is what one request of this kind costs on a muted switch, measured on a
// fake of its own through the production path (what a run pays for the first request).
func datagramsOfOneRequest(t *testing.T, r request) int {
	t.Helper()
	sw := escptest.Start(t)
	sw.Mute()
	c := clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
	if err := r.send(context.Background(), c); !errors.Is(err, escp.ErrNoReply) && !strings.Contains(fmt.Sprint(err), "no reply") {
		t.Fatalf("%s on a muted switch: got %v, want the timeout", r.name, err)
	}
	n := sw.Received()
	if n == 0 {
		t.Fatalf("%s never reached the switch: this proves nothing", r.name)
	}
	return n
}

// tally sorts the outcomes of callers of a muted switch: the request that was sent and timed out,
// the ones refused by the latch, and anything else.
func tally(errs []error) (timedOut, refused, other int) {
	for _, err := range errs {
		switch {
		case err != nil && strings.Contains(err.Error(), latchedErrorText):
			refused++
		case err != nil && (errors.Is(err, escp.ErrNoReply) || strings.Contains(err.Error(), "no reply")):
			timedOut++
		default:
			other++
		}
	}
	return timedOut, refused, other
}

// N callers of one silent switch cost one request: one of them is sent and times out, whichever it
// is, and the others, which were waiting behind it, are refused with the documented error and send
// nothing, whatever kind of request each is.
func TestParallelCallersOfASilentSwitchAreAskedOnce(t *testing.T) {
	for _, r := range []request{readInfo, readConfig} {
		for _, n := range []int{2, 5, 10} {
			t.Run(fmt.Sprintf("%s, %d callers", r.name, n), func(t *testing.T) {
				want := datagramsOfOneRequest(t, r)
				sw := escptest.Start(t)
				sw.Mute()
				c := clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
				errs := inParallel(t, n, func(int) error { return r.send(context.Background(), c) })
				timedOut, refused, other := tally(errs)
				if timedOut != 1 || refused != n-1 || other != 0 {
					t.Fatalf("%d caller(s) timed out, %d were refused, %d other: want 1, %d and 0 (%v)", timedOut, refused, other, n-1, errs)
				}
				if got := sw.Received(); got != want {
					t.Fatalf("%d datagram(s) for %d parallel callers, want the %d of one request", got, n, want)
				}
			})
		}
	}
}

// The callers of every kind at once: reads of two kinds and the configuration read, in parallel, on a
// silent switch, are one request in all.
func TestParallelCallersOfDifferentKindsOnASilentSwitchAreAskedOnce(t *testing.T) {
	want := datagramsOfOneRequest(t, readInfo)
	if other := datagramsOfOneRequest(t, readConfig); other != want {
		t.Fatalf("a read costs %d datagram(s) and the configuration read %d: the count below needs one number", want, other)
	}
	kinds := []request{readInfo, readLED, readConfig}
	sw := escptest.Start(t)
	sw.Mute()
	c := clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
	errs := inParallel(t, 9, func(i int) error { return kinds[i%len(kinds)].send(context.Background(), c) })
	if timedOut, refused, other := tally(errs); timedOut != 1 || refused != 8 || other != 0 {
		t.Fatalf("%d caller(s) timed out, %d were refused, %d other: want 1, 8 and 0 (%v)", timedOut, refused, other, errs)
	}
	if got := sw.Received(); got != want {
		t.Fatalf("%d datagram(s), want the %d of one request", got, want)
	}
}

// The same through Configure: the provider's own switch and a switch of the map each cost one
// request for ten callers, each with its latch.
func TestConfigureLatchesParallelCallersOfEachSilentSwitch(t *testing.T) {
	want := datagramsOfOneRequest(t, readInfo)
	own, mapped := escptest.Start(t), escptest.Start(t)
	own.Mute()
	mapped.Mute()
	fakes := map[string]*escptest.Switch{"192.0.2.1": own, "192.0.2.2": mapped}
	reg := configureRegistry(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, fakes[cfg.Host], cfg) },
		"192.0.2.1", map[string]string{"office": "192.0.2.2"})
	for _, tt := range []struct {
		name string
		sw   *escptest.Switch
	}{{defaultSwitchName, own}, {"office", mapped}} {
		t.Run(tt.name, func(t *testing.T) {
			c := clientOf(t, reg, tt.name)
			errs := inParallel(t, 10, func(int) error { return readInfo.send(context.Background(), c) })
			if timedOut, refused, other := tally(errs); timedOut != 1 || refused != 9 || other != 0 {
				t.Fatalf("%d caller(s) timed out, %d were refused, %d other: want 1, 9 and 0 (%v)", timedOut, refused, other, errs)
			}
			if got := tt.sw.Received(); got != want {
				t.Fatalf("%d datagram(s), want the %d of one request", got, want)
			}
		})
	}
}

// A switch that answers is not latched by load: every one of the parallel callers is answered, the
// configuration read behind the gate (two admissions in a row, the firmware and then the file)
// among them, and nothing deadlocks.
func TestParallelCallersOfAnAnsweringSwitchAreAllAnswered(t *testing.T) {
	kinds := []request{readInfo, readLED, readConfig}
	for _, n := range []int{2, 10, 30} {
		t.Run(fmt.Sprintf("%d callers", n), func(t *testing.T) {
			sw := escptest.Start(t)
			c := clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
			for i, err := range inParallel(t, n, func(i int) error { return kinds[i%len(kinds)].send(context.Background(), c) }) {
				if err != nil {
					t.Errorf("caller %d (%s): %v", i, kinds[i%len(kinds)].name, err)
				}
			}
		})
	}
}

// Two switches are two latches, under load too: ten parallel callers of each at the same moment, one
// silent and one answering. The silent one costs one request and refuses the rest; the answering
// one answers every caller.
func TestParallelCallersOfTwoSwitchesAreLatchedOnTheirOwn(t *testing.T) {
	want := datagramsOfOneRequest(t, readInfo)
	silent, live := escptest.Start(t), escptest.Start(t)
	silent.Mute()
	fakes := map[string]*escptest.Switch{"192.0.2.1": silent, "192.0.2.2": live}
	reg := registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, fakes[cfg.Host], cfg) },
		switchSpec{name: "silent", host: "192.0.2.1"}, switchSpec{name: "live", host: "192.0.2.2"})
	silentClient, liveClient := clientOf(t, reg, "silent"), clientOf(t, reg, "live")
	errs := inParallel(t, 20, func(i int) error {
		if i%2 == 0 {
			return readInfo.send(context.Background(), silentClient)
		}
		return readInfo.send(context.Background(), liveClient)
	})
	var silentErrs []error
	for i, err := range errs {
		if i%2 == 1 && err != nil {
			t.Errorf("caller %d of the answering switch: %v", i, err)
		}
		if i%2 == 0 {
			silentErrs = append(silentErrs, err)
		}
	}
	if timedOut, refused, other := tally(silentErrs); timedOut != 1 || refused != 9 || other != 0 {
		t.Fatalf("silent switch: %d caller(s) timed out, %d were refused, %d other: want 1, 9 and 0 (%v)", timedOut, refused, other, silentErrs)
	}
	if got := silent.Received(); got != want {
		t.Fatalf("%d datagram(s) to the silent switch, want the %d of one request", got, want)
	}
}

// heldClient is a raw client for the tests that need to know exactly when a request is in flight:
// LED enters, tells the test, and ends with what the test sends it. The registry wraps it like any
// client.
type heldClient struct {
	SwitchClient
	entered  chan struct{} // one value per LED call that reached the client
	finish   chan error    // a call in flight ends with what it receives
	calls    atomic.Int32
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func newHeldClient() *heldClient {
	return &heldClient{entered: make(chan struct{}, 16), finish: make(chan error)}
}

func (h *heldClient) Close() error { return nil }

func (h *heldClient) LED(context.Context) (bool, error) {
	h.calls.Add(1)
	n := h.inFlight.Add(1)
	defer h.inFlight.Add(-1)
	atomicMax(&h.maxSeen, n)
	h.entered <- struct{}{}
	return true, <-h.finish
}

func heldRegistry(t *testing.T, h SwitchClient) SwitchClient {
	t.Helper()
	return clientOf(t, registryOf(t, func(escp.Config) (SwitchClient, error) { return h, nil }, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
}

// atomicMax raises v to n when n is larger.
func atomicMax(v *atomic.Int32, n int32) {
	for m := v.Load(); n > m; m = v.Load() {
		if v.CompareAndSwap(m, n) {
			return
		}
	}
}

// result is what a goroutine of these tests reports.
type result struct{ err error }

// callLED sends an LED read in a goroutine and returns the channel of its result.
func callLED(ctx context.Context, c SwitchClient) <-chan result {
	out := make(chan result, 1)
	go func() {
		_, err := c.LED(ctx)
		out <- result{err}
	}()
	return out
}

// await receives a result, failing the test when the caller does not return.
func await(t *testing.T, what string, ch <-chan result) error {
	t.Helper()
	select {
	case r := <-ch:
		return r.err
	case <-time.After(stuckBound):
		t.Fatalf("%s did not return", what)
		return nil
	}
}

// watchedContext is a cancelable context that tells the test when the code under test has started
// to wait on it: Done is what a select reads when it parks, so a caller that never calls it cannot
// be woken by a cancel.
type watchedContext struct {
	waiting    chan struct{} // closed by the first call of Done
	waitOnce   sync.Once
	done       chan struct{} // closed by cancel
	cancelOnce sync.Once
}

func newWatchedContext() *watchedContext {
	return &watchedContext{waiting: make(chan struct{}), done: make(chan struct{})}
}

func (w *watchedContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (w *watchedContext) Done() <-chan struct{} {
	w.waitOnce.Do(func() { close(w.waiting) })
	return w.done
}

func (w *watchedContext) Err() error {
	select {
	case <-w.done:
		return context.Canceled
	default:
		return nil
	}
}

func (w *watchedContext) Value(any) any { return nil }

func (w *watchedContext) cancel() { w.cancelOnce.Do(func() { close(w.done) }) }

// awaitWaiting waits until the caller of ctx is parked on it.
func awaitWaiting(t *testing.T, ctx *watchedContext) {
	t.Helper()
	select {
	case <-ctx.waiting:
	case <-time.After(stuckBound):
		t.Fatal("the waiter never waited on its context: a cancel could not wake it")
	}
}

// awaitEntered waits until a request has reached the client.
func awaitEntered(t *testing.T, h *heldClient) {
	t.Helper()
	select {
	case <-h.entered:
	case <-time.After(stuckBound):
		t.Fatal("no request reached the client")
	}
}

// A request that waits for its turn honors its context: canceled while the request in front of it is
// in flight, it returns the error of its context at once, without reaching the client, without ever
// holding the admission, and the next request is served when the one in flight ends. The request in
// flight is not ended until the canceled waiter has returned, so a waiter that ignored its context
// would hang.
func TestAWaiterThatIsCanceledReturnsItsContextErrorAndTheNextIsServed(t *testing.T) {
	for _, tt := range []struct {
		name       string
		inFlight   error // how the request in front ends
		wantNext   func(error) bool
		wantCalled int32 // calls that reached the client in all
	}{
		{"the request in front is answered", nil, func(err error) bool { return err == nil }, 2},
		{"the request in front is not", fmt.Errorf("lost: %w", escp.ErrNoReply), func(err error) bool { return err != nil && strings.Contains(err.Error(), latchedErrorText) }, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHeldClient()
			c := heldRegistry(t, h)
			front := callLED(context.Background(), c)
			awaitEntered(t, h)

			ctx := newWatchedContext()
			waiter := callLED(ctx, c)
			awaitWaiting(t, ctx) // parked behind the request in front, so the cancel has to wake it
			next := callLED(context.Background(), c)
			ctx.cancel()
			if err := await(t, "the canceled waiter", waiter); !errors.Is(err, context.Canceled) {
				t.Fatalf("the canceled waiter: got %v, want its context's error", err)
			}

			h.finish <- tt.inFlight
			if err := await(t, "the request in front", front); !errors.Is(err, tt.inFlight) {
				t.Fatalf("the request in front: got %v, want %v", err, tt.inFlight)
			}
			if tt.wantCalled > 1 {
				awaitEntered(t, h)
				h.finish <- nil
			}
			if err := await(t, "the next waiter", next); !tt.wantNext(err) {
				t.Fatalf("the next waiter: got %v", err)
			}
			if got := h.calls.Load(); got != tt.wantCalled {
				t.Fatalf("%d request(s) reached the client, want %d: the canceled waiter must not be sent", got, tt.wantCalled)
			}
		})
	}
}

// A context that is canceled before the call returns its error without taking the admission, even
// when nothing is in flight, and the admission is still free afterwards.
func TestACanceledCallerNeverTakesTheAdmission(t *testing.T) {
	h := newHeldClient()
	c := heldRegistry(t, h)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := range 3 {
		if err := await(t, "a call with a canceled context", callLED(ctx, c)); !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d with a canceled context: got %v", i, err)
		}
	}
	if got := h.calls.Load(); got != 0 {
		t.Fatalf("%d request(s) of a canceled caller reached the client", got)
	}
	served := callLED(context.Background(), c)
	awaitEntered(t, h)
	h.finish <- nil
	if err := await(t, "the request after the canceled ones", served); err != nil {
		t.Fatalf("the admission was lost to the canceled callers: %v", err)
	}
}

// panicClient panics in its first LED read and answers the next ones.
type panicClient struct {
	SwitchClient
	calls atomic.Int32
}

func (p *panicClient) Close() error { return nil }

func (p *panicClient) LED(context.Context) (bool, error) {
	if p.calls.Add(1) == 1 {
		panic("the client panicked")
	}
	return true, nil
}

// The admission is released however the request ends: a panic of the client underneath does not
// keep the next request out, and a request refused by the latch does not keep the next one out
// either.
func TestTheAdmissionIsReleasedWhenARequestEndsInAPanicOrARefusal(t *testing.T) {
	t.Run("a panic", func(t *testing.T) {
		p := &panicClient{}
		c := heldRegistry(t, p)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("the panic of the client must reach the caller")
				}
			}()
			_, _ = c.LED(context.Background())
		}()
		next := callLED(context.Background(), c)
		if err := await(t, "the request after the panic", next); err != nil {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("a refusal", func(t *testing.T) {
		h := newHeldClient()
		c := heldRegistry(t, h)
		first := callLED(context.Background(), c)
		awaitEntered(t, h)
		h.finish <- fmt.Errorf("lost: %w", escp.ErrNoReply)
		_ = await(t, "the first request", first)
		for i := range 5 { // a leak would stop the second refusal
			if err := await(t, fmt.Sprintf("refusal %d", i), callLED(context.Background(), c)); err == nil || !strings.Contains(err.Error(), latchedErrorText) {
				t.Fatalf("refusal %d: got %v", i, err)
			}
		}
	})
}

// instantClient answers at once (or, for its first request, with the given error), and measures how
// many requests it has in flight at the same moment.
type instantClient struct {
	SwitchClient
	firstErr error
	calls    atomic.Int32
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func (c *instantClient) Close() error { return nil }

func (c *instantClient) LED(context.Context) (bool, error) {
	n := c.inFlight.Add(1)
	defer c.inFlight.Add(-1)
	atomicMax(&c.maxSeen, n)
	runtime.Gosched() // lets another request in if the admission lets it
	if c.calls.Add(1) == 1 {
		return true, c.firstErr
	}
	return true, nil
}

// Whatever the interleaving of callers, canceled callers and the one request that fails, the
// invariants hold: one request is in flight at a time, a latched switch is never asked again, every
// caller returns, and the admission is free at the end. The interleavings come from seeded
// pseudo-random choices (which caller is canceled before it starts, which after) and from the
// scheduler; the invariants are the same for each of them.
func TestTheAdmissionHoldsUnderRandomCancelsAndFailures(t *testing.T) {
	const callers = 24
	for _, tt := range []struct {
		name     string
		firstErr error
	}{
		{"a switch that answers", nil},
		{"a switch that does not", fmt.Errorf("lost: %w", escp.ErrNoReply)},
	} {
		for seed := uint64(1); seed <= 8; seed++ {
			t.Run(fmt.Sprintf("%s, seed %d", tt.name, seed), func(t *testing.T) {
				rng := rand.New(rand.NewPCG(seed, seed*7919))
				ic := &instantClient{firstErr: tt.firstErr}
				c := heldRegistry(t, ic)
				plan := make([]int, callers) // 0: not canceled, 1: canceled before, 2: canceled concurrently
				for i := range plan {
					plan[i] = rng.IntN(3)
				}
				errs := inParallel(t, callers, func(i int) error {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					switch plan[i] {
					case 1:
						cancel()
					case 2:
						go cancel()
					}
					_, err := c.LED(ctx)
					return err
				})
				for i, err := range errs {
					switch {
					case err == nil:
					case errors.Is(err, context.Canceled) && plan[i] != 0:
					case strings.Contains(err.Error(), latchedErrorText) && tt.firstErr != nil:
					case errors.Is(err, escp.ErrNoReply) && tt.firstErr != nil:
					default:
						t.Errorf("caller %d (plan %d): unexpected %v", i, plan[i], err)
					}
				}
				if got := ic.maxSeen.Load(); got > 1 {
					t.Errorf("%d requests were in flight at once", got)
				}
				if tt.firstErr != nil && ic.calls.Load() > 1 {
					t.Errorf("%d request(s) reached a switch that did not answer the first", ic.calls.Load())
				}
				// the admission is free: a plain call returns (the latched error or an answer), it does not block
				if err := await(t, "the final request", callLED(context.Background(), c)); err != nil && tt.firstErr == nil {
					t.Errorf("the final request: %v", err)
				}
			})
		}
	}
}
