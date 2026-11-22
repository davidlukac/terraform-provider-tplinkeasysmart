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
//
// This file and admission_terraform_test.go are named to sort before every other test file of the
// package: go test runs the files in name order, and a broken admission hangs every later test that
// sends two requests on a client, so the tests that name the fault have to be the ones that run
// first (each call into the latch in them has a watchdog, see within).
//
// Reads and writes take different wrappers (do1 and do0), so every behavior of the admission is
// tested for both: a read is LED, a write is SetLED, real methods of the client.

// stuckBound is how long a test waits for callers that should have returned before it fails: the
// bound of a deadlock, never an expected wait.
const stuckBound = 5 * time.Second

// hangBound is how long a test of the latch may take at most. It is a hang detector, far above the
// seconds the slowest of them needs, and only the last line of defense: the calls into the latch
// have their own, shorter watchdogs (within, await).
const hangBound = 60 * time.Second

// failIfStuck ends the test binary with a message that names the test when the test has not
// finished within bound. A test whose own goroutine is blocked in the latch (a synchronous call,
// or a Terraform run that drives the provider from outside the test) cannot fail itself, and
// without this the binary would hang until its timeout, with the failure in whichever test is
// running by then.
func failIfStuck(t *testing.T, bound time.Duration) {
	t.Helper()
	finished := make(chan struct{})
	t.Cleanup(func() { close(finished) })
	go func() {
		select {
		case <-finished:
		case <-time.After(bound):
			panic(fmt.Sprintf("%s did not finish within %v: a request of the latch never returned", t.Name(), bound))
		}
	}()
}

// within runs fn and fails the test, naming what, when fn does not return within stuckBound. Every
// call a test makes into the latch that could block goes through it (or through await), so that a
// broken admission fails the test that exposes it, at once and by name, and never as a hang that
// ends at the timeout of the whole test binary in some other test.
func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(stuckBound):
		t.Fatalf("%s did not return within %v: the admission of the latch is stuck", what, stuckBound)
	}
}

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
	within(t, fmt.Sprintf("%d parallel callers", n), func() { close(start); wg.Wait() })
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
	readLED  = request{"a read of the LED", func(ctx context.Context, c SwitchClient) error { _, err := c.LED(ctx); return err }}
	// writeLED is the write: it takes do0, the wrapper of every Set, Delete and Enable method.
	writeLED = request{"a write", func(ctx context.Context, c SwitchClient) error { return c.SetLED(ctx, true) }}
	// The configuration read passes the firmware gate, which reads the firmware through the latch and
	// then reads the file through it: two admissions in a row of one call.
	readConfig = request{"the configuration read behind the gate", func(ctx context.Context, c SwitchClient) error {
		_, err := c.ConfigBackup(ctx)
		return err
	}}

	// bothKinds is what the tests of the admission are tabled over: do1 (reads) and do0 (writes).
	bothKinds = []request{readLED, writeLED}
)

// result is what a goroutine of these tests reports.
type result struct{ err error }

// callRequest sends r in a goroutine and returns the channel of its result. A panic of the client
// underneath is reported as an error that says "panic".
func callRequest(ctx context.Context, r request, c SwitchClient) <-chan result {
	out := make(chan result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				out <- result{fmt.Errorf("panic: %v", p)}
			}
		}()
		out <- result{r.send(ctx, c)}
	}()
	return out
}

// await receives a result, failing the test, naming what, when the caller does not return.
func await(t *testing.T, what string, ch <-chan result) error {
	t.Helper()
	select {
	case r := <-ch:
		return r.err
	case <-time.After(stuckBound):
		t.Fatalf("%s did not return within %v: the admission of the latch is stuck", what, stuckBound)
		return nil
	}
}

// sendWithin sends r on c and waits for the answer, with the watchdog.
func sendWithin(t *testing.T, r request, c SwitchClient) error {
	t.Helper()
	return await(t, r.name, callRequest(context.Background(), r, c))
}

// silentClient is a client of a muted fake switch, built through the production path.
func silentClient(t *testing.T) (SwitchClient, *escptest.Switch) {
	t.Helper()
	sw := escptest.Start(t)
	sw.EnableState()
	sw.Mute()
	return clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName), sw
}

// datagramsOfOneRequest is what one request of this kind costs on a muted switch, measured on a
// fake of its own through the production path (what a run pays for the first request).
func datagramsOfOneRequest(t *testing.T, r request) int {
	t.Helper()
	c, sw := silentClient(t)
	if err := sendWithin(t, r, c); !errors.Is(err, escp.ErrNoReply) && !strings.Contains(fmt.Sprint(err), "no reply") {
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
	for _, r := range []request{readInfo, writeLED, readConfig} {
		for _, n := range []int{2, 5, 10} {
			t.Run(fmt.Sprintf("%s, %d callers", r.name, n), func(t *testing.T) {
				want := datagramsOfOneRequest(t, r)
				c, sw := silentClient(t)
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

// The callers of every kind at once, a write racing the reads among them: on a silent switch they
// are one request in all, whichever of them is first (the one that is first is the one that times
// out, and that is the only one that sends). The kinds cost the same number of datagrams, so the
// count does not depend on who is first.
func TestParallelCallersOfDifferentKindsOnASilentSwitchAreAskedOnce(t *testing.T) {
	kinds := []request{readInfo, readLED, readConfig, writeLED}
	want := datagramsOfOneRequest(t, kinds[0])
	for _, k := range kinds[1:] {
		if got := datagramsOfOneRequest(t, k); got != want {
			t.Fatalf("%s costs %d datagram(s) and %s %d: the count below needs one number", kinds[0].name, want, k.name, got)
		}
	}
	c, sw := silentClient(t)
	errs := inParallel(t, 12, func(i int) error { return kinds[i%len(kinds)].send(context.Background(), c) })
	if timedOut, refused, other := tally(errs); timedOut != 1 || refused != 11 || other != 0 {
		t.Fatalf("%d caller(s) timed out, %d were refused, %d other: want 1, 11 and 0 (%v)", timedOut, refused, other, errs)
	}
	if got := sw.Received(); got != want {
		t.Fatalf("%d datagram(s), want the %d of one request", got, want)
	}
	if sw.WriteCount() != 0 {
		t.Fatal("a write reached the state of a muted switch")
	}
}

// A write that gets no reply latches the switch, like a read that gets none: the request after it is
// refused and sends nothing (a switch that drops the writes it is sent is as silent as one that is
// off, for what the write was meant to do). This is the rule of the latch for every kind of request.
func TestAWriteThatGetsNoReplyLatchesTheSwitch(t *testing.T) {
	sw := escptest.Start(t)
	sw.EnableState()
	sw.Configure(func(f *escptest.Faults) { f.DropSets = true })
	c := clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
	if err := sendWithin(t, writeLED, c); !errors.Is(err, escp.ErrWriteUnconfirmed) {
		t.Fatalf("the write: got %v, want the unconfirmed write", err)
	}
	before := sw.Received()
	if err := sendWithin(t, readLED, c); err == nil || !strings.Contains(err.Error(), latchedErrorText) {
		t.Fatalf("the read after it: got %v, want the latched error", err)
	}
	if got := sw.Received(); got != before {
		t.Fatalf("%d datagram(s) went to a switch whose write was not answered", got-before)
	}
}

// The same through Configure: the provider's own switch and a switch of the map each cost one
// request for ten callers, each with its latch.
func TestConfigureLatchesParallelCallersOfEachSilentSwitch(t *testing.T) {
	for _, r := range []request{readInfo, writeLED} {
		t.Run(r.name, func(t *testing.T) {
			want := datagramsOfOneRequest(t, r)
			own, mapped := escptest.Start(t), escptest.Start(t)
			for _, sw := range []*escptest.Switch{own, mapped} {
				sw.EnableState()
				sw.Mute()
			}
			fakes := map[string]*escptest.Switch{"192.0.2.1": own, "192.0.2.2": mapped}
			reg := configureRegistry(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, fakes[cfg.Host], cfg) },
				"192.0.2.1", map[string]string{"office": "192.0.2.2"})
			for _, tt := range []struct {
				name string
				sw   *escptest.Switch
			}{{defaultSwitchName, own}, {"office", mapped}} {
				t.Run(tt.name, func(t *testing.T) {
					c := clientOf(t, reg, tt.name)
					errs := inParallel(t, 10, func(int) error { return r.send(context.Background(), c) })
					if timedOut, refused, other := tally(errs); timedOut != 1 || refused != 9 || other != 0 {
						t.Fatalf("%d caller(s) timed out, %d were refused, %d other: want 1, 9 and 0 (%v)", timedOut, refused, other, errs)
					}
					if got := tt.sw.Received(); got != want {
						t.Fatalf("%d datagram(s), want the %d of one request", got, want)
					}
				})
			}
		})
	}
}

// A switch that answers is not latched by load: every one of the parallel callers is answered, reads,
// writes and the configuration read behind the gate (two admissions in a row, the firmware and then
// the file) among them, and nothing deadlocks.
func TestParallelCallersOfAnAnsweringSwitchAreAllAnswered(t *testing.T) {
	kinds := []request{readInfo, readLED, readConfig, writeLED}
	for _, n := range []int{2, 10, 30} {
		t.Run(fmt.Sprintf("%d callers", n), func(t *testing.T) {
			sw := escptest.Start(t)
			sw.EnableState()
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
	for _, r := range []request{readInfo, writeLED} {
		t.Run(r.name, func(t *testing.T) {
			want := datagramsOfOneRequest(t, r)
			silent, live := escptest.Start(t), escptest.Start(t)
			silent.EnableState()
			live.EnableState()
			silent.Mute()
			fakes := map[string]*escptest.Switch{"192.0.2.1": silent, "192.0.2.2": live}
			reg := registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, fakes[cfg.Host], cfg) },
				switchSpec{name: "silent", host: "192.0.2.1"}, switchSpec{name: "live", host: "192.0.2.2"})
			silentClient, liveClient := clientOf(t, reg, "silent"), clientOf(t, reg, "live")
			errs := inParallel(t, 20, func(i int) error {
				if i%2 == 0 {
					return r.send(context.Background(), silentClient)
				}
				return r.send(context.Background(), liveClient)
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
		})
	}
}

// A raw client for the tests that need to know exactly what reaches it. Its reads (LED) and its
// writes (SetLED) both end in serve; each test gives it the behavior it needs.
type doubleClient struct {
	SwitchClient
	serve    func(n int32) error // the result of the n-th request that reached the client
	calls    atomic.Int32
	inFlight atomic.Int32
	maxSeen  atomic.Int32
}

func (d *doubleClient) Close() error { return nil }

func (d *doubleClient) LED(context.Context) (bool, error) { return true, d.enter() }

func (d *doubleClient) SetLED(context.Context, bool) error { return d.enter() }

func (d *doubleClient) enter() error {
	n := d.calls.Add(1)
	cur := d.inFlight.Add(1)
	defer d.inFlight.Add(-1)
	atomicMax(&d.maxSeen, cur)
	runtime.Gosched() // lets another request in if the admission lets it
	return d.serve(n)
}

// atomicMax raises v to n when n is larger.
func atomicMax(v *atomic.Int32, n int32) {
	for m := v.Load(); n > m; m = v.Load() {
		if v.CompareAndSwap(m, n) {
			return
		}
	}
}

// answering is the behavior of a client that answers every request.
func answering(int32) error { return nil }

// lost is the error of a request that got no reply.
func lost() error { return fmt.Errorf("lost: %w", escp.ErrNoReply) }

// heldClient holds every request in flight until the test ends it: a request enters, tells the test,
// and ends with what the test sends on finish.
func heldClient(finish <-chan error, entered chan<- struct{}) *doubleClient {
	return &doubleClient{serve: func(int32) error {
		entered <- struct{}{}
		return <-finish
	}}
}

// registryClient builds the client of the double through the production path. With allowUnverified
// there is no gate, so the client is the latched client itself (latchOf reads its latch).
func registryClient(t *testing.T, raw SwitchClient, allowUnverified bool) SwitchClient {
	t.Helper()
	return clientOf(t, registryOf(t, func(escp.Config) (SwitchClient, error) { return raw, nil }, switchSpec{host: "192.0.2.1", allowUnverified: allowUnverified}), defaultSwitchName)
}

func heldRegistry(t *testing.T, raw SwitchClient) SwitchClient {
	t.Helper()
	return registryClient(t, raw, false)
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
func awaitEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(stuckBound):
		t.Fatal("no request reached the client")
	}
}

// parkWaiters sends n requests of kind r that wait behind the one in flight, each with a context
// that tells when it is parked, and returns once all of them are parked.
func parkWaiters(t *testing.T, r request, c SwitchClient, n int) []<-chan result {
	t.Helper()
	out := make([]<-chan result, n)
	for i := range out {
		ctx := newWatchedContext()
		out[i] = callRequest(ctx, r, c)
		awaitWaiting(t, ctx)
	}
	return out
}

// A request that waits for its turn honors its context: canceled while the request in front of it is
// in flight, it returns the error of its context at once, without reaching the client, and the next
// request is served when the one in flight ends. The request in flight is not ended until the
// canceled waiter has returned, so a waiter that ignored its context would hang.
func TestAWaiterThatIsCanceledReturnsItsContextErrorAndTheNextIsServed(t *testing.T) {
	for _, r := range bothKinds {
		for _, tt := range []struct {
			name       string
			inFront    error // how the request in front ends
			wantNext   func(error) bool
			wantCalled int32 // calls that reached the client in all
		}{
			{"the request in front is answered", nil, func(err error) bool { return err == nil }, 2},
			{"the request in front is not", lost(), func(err error) bool { return err != nil && strings.Contains(err.Error(), latchedErrorText) }, 1},
		} {
			t.Run(r.name+", "+tt.name, func(t *testing.T) {
				finish, entered := make(chan error), make(chan struct{}, 16)
				h := heldClient(finish, entered)
				c := heldRegistry(t, h)
				front := callRequest(context.Background(), r, c)
				awaitEntered(t, entered)

				ctx := newWatchedContext()
				waiter := callRequest(ctx, r, c)
				awaitWaiting(t, ctx) // parked behind the request in front, so the cancel has to wake it
				next := callRequest(context.Background(), r, c)
				ctx.cancel()
				if err := await(t, "the canceled waiter", waiter); !errors.Is(err, context.Canceled) {
					t.Fatalf("the canceled waiter: got %v, want its context's error", err)
				}

				finish <- tt.inFront
				if err := await(t, "the request in front", front); !errors.Is(err, tt.inFront) {
					t.Fatalf("the request in front: got %v, want %v", err, tt.inFront)
				}
				if tt.wantCalled > 1 {
					awaitEntered(t, entered)
					finish <- nil
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
}

// A context that is canceled before the call is refused with its error and never takes the
// admission: the request does not reach the client, free slot or not. The slot is free here, and the
// choice between a free slot and a canceled context is a select that picks at random when both are
// ready, so the early check is what makes the outcome certain: sixty-four calls leave no room for
// luck (a latch without the check would send about half of them).
func TestACallWithACanceledContextNeverReachesTheClient(t *testing.T) {
	for _, r := range bothKinds {
		t.Run(r.name, func(t *testing.T) {
			d := &doubleClient{serve: answering}
			c := heldRegistry(t, d)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for i := range 64 {
				if err := await(t, "a call with a canceled context", callRequest(ctx, r, c)); !errors.Is(err, context.Canceled) {
					t.Fatalf("call %d: got %v, want the context's error", i, err)
				}
			}
			if got := d.calls.Load(); got != 0 {
				t.Fatalf("%d request(s) of a canceled caller reached the client", got)
			}
			if err := sendWithin(t, r, c); err != nil {
				t.Fatalf("the admission was lost to the canceled callers: %v", err)
			}
		})
	}
}

// The admission is released however a request ends, for a read and for a write: an answer, an error
// that says nothing about the switch, a panic of the client underneath, and a refusal by the latch
// itself. Each one is followed by a request that must be served (or, after a refusal, refused again
// and not hang).
func TestTheAdmissionIsReleasedWhenARequestEnds(t *testing.T) {
	for _, r := range bothKinds {
		for _, tt := range []struct {
			name      string
			first     func() error
			wantFirst string // a text of the error of the first request
			wantLater string // "" when later requests are answered, else a text of their error
		}{
			{"an answer", func() error { return nil }, "", ""},
			{"an error that says nothing about the switch", func() error { return errors.New("an invalid argument") }, "an invalid argument", ""},
			{"a panic of the client", func() error { panic("the client panicked") }, "panic: the client panicked", ""},
			{"no reply, then refusals", lost, "lost", latchedErrorText},
		} {
			t.Run(r.name+", "+tt.name, func(t *testing.T) {
				d := &doubleClient{serve: func(n int32) error {
					if n == 1 {
						return tt.first()
					}
					return nil
				}}
				c := heldRegistry(t, d)
				err := sendWithin(t, r, c)
				if (tt.wantFirst == "") != (err == nil) || (tt.wantFirst != "" && !strings.Contains(err.Error(), tt.wantFirst)) {
					t.Fatalf("the first request: got %v, want %q", err, tt.wantFirst)
				}
				for i := range 5 { // a leak would stop the second request
					err := sendWithin(t, r, c)
					if (tt.wantLater == "") != (err == nil) || (tt.wantLater != "" && !strings.Contains(err.Error(), tt.wantLater)) {
						t.Fatalf("request %d after it: got %v, want %q", i+2, err, tt.wantLater)
					}
				}
				if tt.wantLater != "" && d.calls.Load() != 1 {
					t.Fatalf("%d request(s) reached the client, want only the first", d.calls.Load())
				}
			})
		}
	}
}

// outcomeError is the error of a request that got no reply, which tells the test whether the
// admission was still held at the moment the latch looked at it: errors.Is is what record calls, and
// it is called while the outcome is recorded.
type outcomeError struct {
	latch   *failFastLatch
	checked *atomic.Int32 // how many times the latch looked at the error
	free    *atomic.Int32 // ... and found the admission already released
	over    *atomic.Bool  // the request has returned: what looks at the error from now on is the test
}

func (e outcomeError) Error() string { return "lost: " + escp.ErrNoReply.Error() }

func (e outcomeError) Unwrap() error { return escp.ErrNoReply }

func (e outcomeError) Is(error) bool {
	if e.over.Load() {
		return false
	}
	e.checked.Add(1)
	if len(e.latch.admission()) == 0 {
		e.free.Add(1)
	}
	return false
}

// The outcome of a request is recorded before the next request is admitted: what a waiter parked
// behind a failing request sees is the failure, never the state before it. The admission is still
// held while record looks at the error. (A latch that released first and recorded after would let
// the next request pass the check in between, and the first wave of a run through again, as a race.)
func TestTheOutcomeIsRecordedWhileTheAdmissionIsStillHeld(t *testing.T) {
	for _, r := range bothKinds {
		t.Run(r.name, func(t *testing.T) {
			var checked, free atomic.Int32
			var over atomic.Bool
			d := &doubleClient{}
			c := registryClient(t, d, true) // no gate: the client is the latched client
			oe := outcomeError{latch: mustAs[latchedClient](t, c).latch, checked: &checked, free: &free, over: &over}
			d.serve = func(int32) error { return oe }
			err := sendWithin(t, r, c)
			over.Store(true)
			if !errors.Is(err, escp.ErrNoReply) {
				t.Fatalf("the request: got %v", err)
			}
			if checked.Load() == 0 {
				t.Fatal("the outcome of the request was never recorded")
			}
			if free.Load() != 0 {
				t.Fatal("the admission was released before the outcome was recorded")
			}
			if err := sendWithin(t, r, c); err == nil || !strings.Contains(err.Error(), latchedErrorText) {
				t.Fatalf("the next request: got %v, want the latched error", err)
			}
		})
	}
}

// The same ordering seen from the waiters: six requests parked behind one that gets no answer are
// all refused and none is sent, in each of three hundred rounds (a race window, so a loop; the
// rounds are the same on every run, the interleavings come from the scheduler).
func TestWaitersParkedBehindAFailureAreAllRefused(t *testing.T) {
	const rounds, waiters = 300, 6
	for _, r := range bothKinds {
		t.Run(r.name, func(t *testing.T) {
			for i := range rounds {
				finish, entered := make(chan error), make(chan struct{}, 16)
				h := heldClient(finish, entered)
				c := heldRegistry(t, h)
				front := callRequest(context.Background(), r, c)
				awaitEntered(t, entered)
				parked := parkWaiters(t, r, c, waiters)
				finish <- lost()
				_ = await(t, "the request in front", front)
				for w, ch := range parked {
					if err := await(t, fmt.Sprintf("waiter %d of round %d", w, i), ch); err == nil || !strings.Contains(err.Error(), latchedErrorText) {
						t.Fatalf("round %d, waiter %d: got %v, want the latch's refusal", i, w, err)
					}
				}
				if got := h.calls.Load(); got != 1 {
					t.Fatalf("round %d: %d requests reached the client, want 1", i, got)
				}
			}
		})
	}
}

// Whatever the interleaving of callers, canceled callers and the one request that fails, the
// invariants hold: one request is in flight at a time, a latched switch is never asked again, every
// caller returns, and the admission is free at the end. The interleavings come from seeded
// pseudo-random choices (which caller is canceled before it starts, which after, which is a write)
// and from the scheduler; the invariants are the same for each of them.
func TestTheAdmissionHoldsUnderRandomCancelsAndFailures(t *testing.T) {
	const callers = 24
	for _, tt := range []struct {
		name     string
		firstErr error
	}{
		{"a switch that answers", nil},
		{"a switch that does not", lost()},
	} {
		for seed := uint64(1); seed <= 8; seed++ {
			t.Run(fmt.Sprintf("%s, seed %d", tt.name, seed), func(t *testing.T) {
				rng := rand.New(rand.NewPCG(seed, seed*7919))
				d := &doubleClient{serve: func(n int32) error {
					if n == 1 {
						return tt.firstErr
					}
					return nil
				}}
				c := heldRegistry(t, d)
				plan := make([]int, callers) // 0: not canceled, 1: canceled before, 2: canceled concurrently
				kinds := make([]request, callers)
				for i := range plan {
					plan[i] = rng.IntN(3)
					kinds[i] = bothKinds[rng.IntN(len(bothKinds))]
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
					return kinds[i].send(ctx, c)
				})
				for i, err := range errs {
					switch {
					case err == nil:
					case errors.Is(err, context.Canceled) && plan[i] != 0:
					case strings.Contains(err.Error(), latchedErrorText) && tt.firstErr != nil:
					case errors.Is(err, escp.ErrNoReply) && tt.firstErr != nil:
					default:
						t.Errorf("caller %d (plan %d, %s): unexpected %v", i, plan[i], kinds[i].name, err)
					}
				}
				if got := d.maxSeen.Load(); got > 1 {
					t.Errorf("%d requests were in flight at once", got)
				}
				if tt.firstErr != nil && d.calls.Load() > 1 {
					t.Errorf("%d request(s) reached a switch that did not answer the first", d.calls.Load())
				}
				// the admission is free: a plain call returns (the latched error or an answer), it does not block
				if err := sendWithin(t, bothKinds[0], c); err != nil && tt.firstErr == nil {
					t.Errorf("the final request: %v", err)
				}
			})
		}
	}
}

// A zero latch is ready to use, and releasing an admission that is not held does not block: a
// mistake in the pairing of acquire and release cannot hang a run.
func TestAZeroLatchIsReadyAndReleaseNeverBlocks(t *testing.T) {
	l := &failFastLatch{}
	within(t, "release of an admission that is not held", l.release)
	var err error
	within(t, "the first admission of a zero latch", func() { err = l.acquire(context.Background()) })
	if err != nil {
		t.Fatalf("a zero latch refused the first request: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	within(t, "an admission with a canceled context", func() { err = l.acquire(ctx) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want the context's error", err)
	}
	within(t, "the release of the held admission", l.release)
	within(t, "the second admission", func() { err = l.acquire(context.Background()) })
	if err != nil {
		t.Fatalf("the admission was not handed over: %v", err)
	}
}

// Close is not held up by the callers that are queued in the latch: with one request in flight and
// seven waiting behind it, Close returns, and every one of the eight callers returns with the
// client's ErrClosed, none of them with the latch's refusal (a closed client says nothing about the
// switch). Through the registry, with a real client that would wait twenty seconds for its reply.
func TestCloseDoesNotWaitForTheCallersQueuedInTheLatch(t *testing.T) {
	sw := escptest.Start(t)
	sw.Mute()
	c := clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) {
		cfg.SwitchPort = sw.UDPPort()
		cfg.HostPort = escptest.FreeUDPPort(t)
		cfg.BroadcastAddr = escptest.Loopback()
		cfg.Host = "127.0.0.1"
		cfg.Retries = escp.NoRetries
		cfg.Timeout = 20 * time.Second // the longest the client takes; Close ends the request in flight, the timeout does not
		return escp.Dial(cfg)
	}, switchSpec{host: "192.0.2.1"}), defaultSwitchName)

	front := callRequest(context.Background(), readInfo, c)
	// The request is in flight when its datagram has reached the switch: the state is observed, the
	// clock only bounds the wait.
	deadline := time.Now().Add(stuckBound) // nosleep: a poll of a state that has no signal, bounded
	for sw.Received() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the request in flight never reached the switch")
		}
		runtime.Gosched()
	}
	queued := parkWaiters(t, readInfo, c, 7)

	var closeErr error
	within(t, "Close", func() { closeErr = c.Close() })
	if closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	for i, ch := range append([]<-chan result{front}, queued...) {
		err := await(t, fmt.Sprintf("caller %d", i), ch)
		if !errors.Is(err, escp.ErrClosed) || strings.Contains(err.Error(), latchedErrorText) {
			t.Fatalf("caller %d: got %v, want ErrClosed and no refusal by the latch", i, err)
		}
	}
}
