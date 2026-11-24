package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/model"
)

// A switch that does not answer costs the whole timeout, with its retries, on every request, and
// the requests of one switch are serialized: a refresh of fifteen resources against a switch that
// is off takes minutes before the plan fails. failFast puts a latch in front of a client: when the
// first request that anything of this client sends gets no answer, every other request to that
// switch of the same run, one that is already waiting for its turn included, fails at once with an
// error that says the request was not sent and what kind of failure came first (see notSentError).
// A switch that has answered a request this latch saw is not latched afterwards (a single read it
// does not answer, as some firmware does for some settings, says nothing about the next). What the
// client asks inside a write is not seen by the latch (README, "Safety by design").
//
// The latch admits one request at a time (the library serializes the requests of a client, so no
// throughput is lost) and decides after the admission, when the request is about to be sent: a
// request that queued behind the first one is checked only when its turn comes, which is after the
// first one has failed (#222). The outcome of a request is recorded before the next one is admitted.
// Waiting honors the context of the request. Nothing is held across the request but the admission
// itself, a channel of capacity one, which is released when the request ends, whatever way it ends.
//
// The admission is not re-entrant: a method of the wrapped client that calls another method of the
// wrapped client while it holds the admission waits for itself until its context ends. A wrapped
// method calls the client underneath, never another wrapped method.
//
// The latch lives as long as the client, which is one provider configuration: the next run of
// Terraform asks again. The zero value is ready to use.
type failFastLatch struct {
	once     sync.Once
	slot     chan struct{} // the admission: a request holds the one slot while it is sent; see admission
	mu       sync.Mutex    // guards the fields below; never held while waiting or sending
	answered bool          // some request got an answer
	silence  *silence      // the first request that got no answer, while nothing was answered
}

// A requestKind says what the first request that got no answer was, in the words of a refusal. The
// latch knows two kinds, the reads and the writes, because they take different wrappers (do1, do0).
type requestKind string

const (
	readRequest  requestKind = "a read"
	writeRequest requestKind = "a write"
)

// silence is what the latch keeps of the first request that got no answer: the kind of request and
// the kinds of failure it matched (escp.ErrNoReply, escp.ErrNotFound, or both), never its error.
// The error of a request that was sent says what the library knows of it, and for a write that is
// that it may have been applied; a request that was not sent must not say that or match it.
type silence struct {
	request requestKind
	kinds   []error
}

// notSentError is the error of a request that the latch did not send. It matches the kinds of
// failure of the first request that got no answer, so errors.Is for escp.ErrNoReply and
// escp.ErrNotFound keeps working, and nothing else of it: not escp.ErrWriteUnconfirmed, whatever
// the first request was. Its text names the kind of that request (a read or a write), so that the
// user can find the error of the request that was sent in the same output.
type notSentError struct{ first *silence }

func (r *notSentError) Error() string {
	texts := make([]string, len(r.first.kinds))
	for i, k := range r.first.kinds {
		texts[i] = k.Error()
	}
	return fmt.Sprintf("the switch did not answer earlier in this run, so this request was not sent (the first request without an answer was %s): %s",
		r.first.request, strings.Join(texts, "; "))
}

// Unwrap returns the kinds of failure of the first request, never that request's own error.
func (r *notSentError) Unwrap() []error { return r.first.kinds }

// admission is the channel of the admission, made on first use so that the zero latch works.
func (l *failFastLatch) admission() chan struct{} {
	l.once.Do(func() { l.slot = make(chan struct{}, 1) })
	return l.slot
}

// acquire admits the request: it waits for its turn (and gives up with the error of the context),
// then refuses the request when the switch did not answer before. A nil error means the caller
// holds the admission and must call release when the request has ended; with an error it holds
// nothing. A context that is canceled before the call never takes the slot, whether or not it is
// free; a wait that is canceled just as the slot frees may still take it (the select picks one of
// two ready cases), and the request then ends with the error of the context in the client.
func (l *failFastLatch) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case l.admission() <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := l.refused(); err != nil {
		l.release()
		return err
	}
	return nil
}

// release hands the admission to the next request. It never blocks: releasing an admission that is
// not held does nothing, so a mistake in the pairing cannot hang a run.
func (l *failFastLatch) release() {
	select {
	case <-l.admission():
	default:
	}
}

// refused is the error of a request that is not sent because the switch did not answer before.
func (l *failFastLatch) refused() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.silence == nil {
		return nil
	}
	return &notSentError{first: l.silence}
}

// record learns from the outcome of a request: an answer, a refusal or another device's reply are
// answers; silence and a switch that cannot be found are what the latch is for (while nothing was
// answered); anything else (a canceled call, an invalid argument) says nothing about the switch.
func (l *failFastLatch) record(err error, request requestKind) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil || errors.Is(err, escp.ErrRejected) || errors.Is(err, escp.ErrWrongSwitch) {
		l.answered = true
		return
	}
	if kinds := silentKinds(err); len(kinds) > 0 && !l.answered && l.silence == nil {
		l.silence = &silence{request: request, kinds: kinds}
	}
}

// silentKinds returns the kinds of silence err matches, in a fixed order: none for any other error.
func silentKinds(err error) []error {
	var kinds []error
	for _, kind := range []error{escp.ErrNoReply, escp.ErrNotFound} {
		if errors.Is(err, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// failFast returns c with the latch in front of every request; Close and SwitchMAC pass through
// (they send nothing, so they are not admitted: Close is never held up by the requests that wait).
// The wrapped methods are not re-entrant (see failFastLatch).
func failFast(c SwitchClient) SwitchClient {
	return latchedClient{SwitchClient: c, latch: &failFastLatch{}}
}

type latchedClient struct {
	SwitchClient
	latch *failFastLatch
}

// do0 and do1 run one request through the latch: admitted, sent, recorded, released. A wrapped
// method calls the client underneath and never another wrapped method, so the admission is never
// requested while it is held.
func do0(ctx context.Context, l *failFastLatch, f func() error) error {
	if err := l.acquire(ctx); err != nil {
		return err
	}
	defer l.release()
	err := f()
	l.record(err, writeRequest)
	return err
}

// do1 is do0 for a request that returns a value; the same rule holds: f calls the client
// underneath and never another wrapped method, or the call waits for itself.
func do1[T any](ctx context.Context, l *failFastLatch, f func() (T, error)) (T, error) {
	if err := l.acquire(ctx); err != nil {
		var zero T
		return zero, err
	}
	defer l.release()
	v, err := f()
	l.record(err, readRequest)
	return v, err
}

func (c latchedClient) Info(ctx context.Context) (model.Info, error) {
	return do1(ctx, c.latch, func() (model.Info, error) { return c.SwitchClient.Info(ctx) })
}

func (c latchedClient) LED(ctx context.Context) (bool, error) {
	return do1(ctx, c.latch, func() (bool, error) { return c.SwitchClient.LED(ctx) })
}

func (c latchedClient) SetLED(ctx context.Context, on bool) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetLED(ctx, on) })
}

func (c latchedClient) Ports(ctx context.Context) ([]model.Port, error) {
	return do1(ctx, c.latch, func() ([]model.Port, error) { return c.SwitchClient.Ports(ctx) })
}

func (c latchedClient) SetPort(ctx context.Context, cfg model.PortConfig) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetPort(ctx, cfg) })
}

func (c latchedClient) PortStats(ctx context.Context) ([]model.PortStats, error) {
	return do1(ctx, c.latch, func() ([]model.PortStats, error) { return c.SwitchClient.PortStats(ctx) })
}

func (c latchedClient) LAGs(ctx context.Context) ([]model.LAG, error) {
	return do1(ctx, c.latch, func() ([]model.LAG, error) { return c.SwitchClient.LAGs(ctx) })
}

func (c latchedClient) SetLAG(ctx context.Context, lag model.LAG) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetLAG(ctx, lag) })
}

func (c latchedClient) IGMP(ctx context.Context) (model.IGMP, error) {
	return do1(ctx, c.latch, func() (model.IGMP, error) { return c.SwitchClient.IGMP(ctx) })
}

func (c latchedClient) SetIGMP(ctx context.Context, cfg model.IGMPConfig) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetIGMP(ctx, cfg) })
}

func (c latchedClient) LoopPrevention(ctx context.Context) (bool, error) {
	return do1(ctx, c.latch, func() (bool, error) { return c.SwitchClient.LoopPrevention(ctx) })
}

func (c latchedClient) SetLoopPrevention(ctx context.Context, on bool) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetLoopPrevention(ctx, on) })
}

func (c latchedClient) PortMirror(ctx context.Context) (model.Mirror, error) {
	return do1(ctx, c.latch, func() (model.Mirror, error) { return c.SwitchClient.PortMirror(ctx) })
}

func (c latchedClient) SetPortMirror(ctx context.Context, m model.Mirror) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetPortMirror(ctx, m) })
}

func (c latchedClient) MTUVLAN(ctx context.Context) (model.MTUVLAN, error) {
	return do1(ctx, c.latch, func() (model.MTUVLAN, error) { return c.SwitchClient.MTUVLAN(ctx) })
}

func (c latchedClient) PortBasedVLAN(ctx context.Context) (model.PortBasedVLAN, error) {
	return do1(ctx, c.latch, func() (model.PortBasedVLAN, error) { return c.SwitchClient.PortBasedVLAN(ctx) })
}

func (c latchedClient) SetPortBasedVLAN(ctx context.Context, group model.PortVLAN) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetPortBasedVLAN(ctx, group) })
}

func (c latchedClient) Dot1Q(ctx context.Context) (model.Dot1Q, error) {
	return do1(ctx, c.latch, func() (model.Dot1Q, error) { return c.SwitchClient.Dot1Q(ctx) })
}

func (c latchedClient) VLANMode(ctx context.Context) (model.VLANModeSetting, error) {
	return do1(ctx, c.latch, func() (model.VLANModeSetting, error) { return c.SwitchClient.VLANMode(ctx) })
}

func (c latchedClient) SetVLANMode(ctx context.Context, s model.VLANModeSetting) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetVLANMode(ctx, s) })
}

func (c latchedClient) SetDot1QVLAN(ctx context.Context, v model.Dot1QVLAN) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetDot1QVLAN(ctx, v) })
}

func (c latchedClient) DeleteDot1QVLAN(ctx context.Context, id model.VLANID) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.DeleteDot1QVLAN(ctx, id) })
}

func (c latchedClient) SetPVIDs(ctx context.Context, pvids []model.PortPVID) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetPVIDs(ctx, pvids) })
}

func (c latchedClient) QoS(ctx context.Context) (model.QoS, error) {
	return do1(ctx, c.latch, func() (model.QoS, error) { return c.SwitchClient.QoS(ctx) })
}

func (c latchedClient) SetQoSMode(ctx context.Context, mode model.QoSMode) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetQoSMode(ctx, mode) })
}

func (c latchedClient) SetPortPriorities(ctx context.Context, changes []model.PortPriority) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetPortPriorities(ctx, changes) })
}

func (c latchedClient) Bandwidth(ctx context.Context) ([]model.PortBandwidth, error) {
	return do1(ctx, c.latch, func() ([]model.PortBandwidth, error) { return c.SwitchClient.Bandwidth(ctx) })
}

func (c latchedClient) SetBandwidth(ctx context.Context, limit model.PortBandwidth) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetBandwidth(ctx, limit) })
}

func (c latchedClient) StormControl(ctx context.Context) ([]model.PortStorm, error) {
	return do1(ctx, c.latch, func() ([]model.PortStorm, error) { return c.SwitchClient.StormControl(ctx) })
}

func (c latchedClient) SetStormControl(ctx context.Context, cfg model.StormConfig) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetStormControl(ctx, cfg) })
}

func (c latchedClient) System(ctx context.Context) (model.System, error) {
	return do1(ctx, c.latch, func() (model.System, error) { return c.SwitchClient.System(ctx) })
}

func (c latchedClient) SetHostname(ctx context.Context, name model.Hostname) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetHostname(ctx, name) })
}

func (c latchedClient) SetIPConfig(ctx context.Context, cfg model.IPConfig) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.SetIPConfig(ctx, cfg) })
}

func (c latchedClient) EnableDHCP(ctx context.Context) error {
	return do0(ctx, c.latch, func() error { return c.SwitchClient.EnableDHCP(ctx) })
}

func (c latchedClient) ConfigBackup(ctx context.Context) (model.ConfigFile, error) {
	return do1(ctx, c.latch, func() (model.ConfigFile, error) { return c.SwitchClient.ConfigBackup(ctx) })
}
