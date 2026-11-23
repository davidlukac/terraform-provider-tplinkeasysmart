package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
	"github.com/davidlukac/escp-go/model"
)

// These tests are about what a request that the fail-fast latch refuses says and matches (#224,
// DC-P06, and DC-L02 of the common catalog: the identity and the text of an error say what
// happened). The request was never sent, so its error must say so and must not claim that a write
// may have been applied, whatever the first request was. They go through the production path
// (registryBuilder.add): real clients over UDP to a muted fake switch for the two kinds of first
// request, doubles that the registry wraps like any other for the failures a fake cannot make
// (a switch that cannot be found). Every call into the latch has a watchdog (sendWithin).

// faultyFake is a client of a fake switch with the given fault, built through the production path.
func faultyFake(t *testing.T, fault func(*escptest.Switch)) (SwitchClient, *escptest.Switch) {
	t.Helper()
	sw := escptest.Start(t)
	sw.EnableState()
	fault(sw)
	return clientOf(t, registryOf(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, sw, cfg) }, switchSpec{host: "192.0.2.1"}), defaultSwitchName), sw
}

const (
	notSentText     = "was not sent"
	unconfirmedText = "may have been applied"
)

// The first request that gets no answer is the one that is sent, and a later request is refused: the
// refusal says it was not sent, matches the kind of the first failure and nothing of what only the
// first request knows (it was a write that may have been applied), and the first request keeps its
// own error, whatever kind of request each is.
func TestARefusedRequestSaysItWasNotSentAndIsNotAnUnconfirmedWrite(t *testing.T) {
	failIfStuck(t, hangBound)
	for _, first := range []struct {
		name        string
		fault       func(*escptest.Switch)
		req         request
		unconfirmed bool   // the first request is a write that was sent and got no reply
		kind        string // what a refusal calls the first request
	}{
		{"a read that gets no reply", func(sw *escptest.Switch) { sw.Mute() }, readLED, false, "a read"},
		{"a write that gets no reply", func(sw *escptest.Switch) { sw.Configure(func(f *escptest.Faults) { f.DropSets = true }) }, writeLED, true, "a write"},
		// The firmware gate reads the firmware before it sends the write: no reply to that is no write sent.
		{"a write the gate cannot check", func(sw *escptest.Switch) { sw.Mute() }, writeLED, false, "a write"},
	} {
		for _, second := range []request{readInfo, readLED, writeLED, readConfig} {
			t.Run(first.name+", then "+second.name, func(t *testing.T) {
				c, sw := faultyFake(t, first.fault)
				firstErr := sendWithin(t, first.req, c)
				if !errors.Is(firstErr, escp.ErrNoReply) || strings.Contains(firstErr.Error(), latchedErrorText) {
					t.Fatalf("the first request: got %v, want the timeout itself", firstErr)
				}
				// The first request keeps its own error: a write that was sent is unconfirmed, nothing else is.
				if errors.Is(firstErr, escp.ErrWriteUnconfirmed) != first.unconfirmed || strings.Contains(firstErr.Error(), unconfirmedText) != first.unconfirmed {
					t.Fatalf("the first request (unconfirmed write: %v): got %v, which has the wrong identity or text", first.unconfirmed, firstErr)
				}
				sent := sw.Received()
				if sent == 0 {
					t.Fatal("the first request never reached the switch: this proves nothing")
				}
				err := sendWithin(t, second, c)
				switch {
				case err == nil:
					t.Fatal("the second request was not refused")
				case !errors.Is(err, escp.ErrNoReply):
					t.Errorf("got %v, which does not match ErrNoReply: the kind of the first failure is lost", err)
				case errors.Is(err, escp.ErrWriteUnconfirmed):
					t.Errorf("got %v, which matches ErrWriteUnconfirmed: nothing was sent for this request", err)
				case !strings.Contains(err.Error(), latchedErrorText) || !strings.Contains(err.Error(), notSentText):
					t.Errorf("got %v, want the text to say that the switch did not answer earlier and that the request was not sent", err)
				case strings.Contains(err.Error(), unconfirmedText):
					t.Errorf("got %v, which says that a write may have been applied: nothing was sent for this request", err)
				case !strings.Contains(err.Error(), first.kind):
					t.Errorf("got %v, want it to name the first request that got no answer (%s)", err, first.kind)
				}
				if got := sw.Received(); got != sent {
					t.Fatalf("%d datagram(s) went to a switch that did not answer", got-sent)
				}
			})
		}
	}
}

// A refused request matches exactly the kinds of silence that the first failure matched, whatever
// else the first error was: a write without a reply (both ErrNoReply and ErrWriteUnconfirmed), a
// switch that could not be found (ErrNotFound only), and a failure that is both kinds at once. The
// first request that was sent keeps its own error in every case.
func TestARefusalMatchesTheKindsOfSilenceOfTheFirstFailureAndNothingElse(t *testing.T) {
	failIfStuck(t, hangBound)
	unconfirmed := &escp.UnconfirmedWriteError{Method: model.MethodSetLED, Err: escp.ErrNoReply}
	notFound := fmt.Errorf("%w: 00:00:5e:00:53:01", escp.ErrNotFound)
	for _, tt := range []struct {
		name         string
		first        request
		firstErr     error
		wantNoReply  bool
		wantNotFound bool
		wantKind     string
	}{
		{"a write without a reply", writeLED, unconfirmed, true, false, "a write"},
		{"a read without a reply", readLED, fmt.Errorf("reading: %w", escp.ErrNoReply), true, false, "a read"},
		{"a read of a switch that cannot be found", readLED, notFound, false, true, "a read"},
		{"a write to a switch that cannot be found", writeLED, notFound, false, true, "a write"},
		{"a failure of both kinds", readLED, errors.Join(escp.ErrNoReply, notFound), true, true, "a read"},
	} {
		for _, second := range bothKinds {
			t.Run(tt.name+", then "+second.name, func(t *testing.T) {
				d := &doubleClient{serve: func(int32) error { return tt.firstErr }}
				c := registryClient(t, d, true)
				if err := sendWithin(t, tt.first, c); !errors.Is(err, tt.firstErr) || err.Error() != tt.firstErr.Error() {
					t.Fatalf("the first request: got %v, want its own error unchanged", err)
				}
				err := sendWithin(t, second, c)
				switch {
				case err == nil:
					t.Fatal("the second request was not refused")
				case errors.Is(err, escp.ErrNoReply) != tt.wantNoReply:
					t.Errorf("got %v: errors.Is(ErrNoReply) is %v, want %v", err, !tt.wantNoReply, tt.wantNoReply)
				case errors.Is(err, escp.ErrNotFound) != tt.wantNotFound:
					t.Errorf("got %v: errors.Is(ErrNotFound) is %v, want %v", err, !tt.wantNotFound, tt.wantNotFound)
				case errors.Is(err, escp.ErrWriteUnconfirmed):
					t.Errorf("got %v, which matches ErrWriteUnconfirmed", err)
				case strings.Contains(err.Error(), escp.ErrNoReply.Error()) != tt.wantNoReply || strings.Contains(err.Error(), escp.ErrNotFound.Error()) != tt.wantNotFound:
					t.Errorf("got %v, want the text to name the kinds of silence that the first failure matched, and only those", err)
				case !strings.Contains(err.Error(), notSentText) || strings.Contains(err.Error(), unconfirmedText) || !strings.Contains(err.Error(), tt.wantKind):
					t.Errorf("got %v, want the text to say the request was not sent, name %q and never say a write may have been applied", err, tt.wantKind)
				}
				if got := d.calls.Load(); got != 1 {
					t.Fatalf("%d request(s) reached the client, want only the first", got)
				}
			})
		}
	}
}

// A call that is canceled is not refused: it returns its context's error, which is not a refusal
// and matches no kind of silence, even after a write that got no reply latched the switch.
func TestACanceledWaiterIsNotRefusedAndMatchesNoKindOfSilence(t *testing.T) {
	failIfStuck(t, hangBound)
	d := &doubleClient{serve: func(int32) error {
		return &escp.UnconfirmedWriteError{Method: model.MethodSetLED, Err: escp.ErrNoReply}
	}}
	c := registryClient(t, d, true)
	if err := sendWithin(t, writeLED, c); !errors.Is(err, escp.ErrWriteUnconfirmed) {
		t.Fatalf("the first request: got %v, want its own error", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := await(t, "a request with a canceled context", callRequest(ctx, readLED, c))
	if !errors.Is(err, context.Canceled) || errors.Is(err, escp.ErrNoReply) || strings.Contains(err.Error(), notSentText) {
		t.Fatalf("got %v, want the context's error and nothing of the latch", err)
	}
}

// The latch keeps the first failure: a later outcome neither replaces what a refusal says nor adds
// a kind to what it matches. Through the production path no request is sent after the first
// failure, so only a call of record itself can tell a latch that keeps the first from one that
// keeps the last.
func TestTheLatchKeepsTheFirstFailureForEveryRefusal(t *testing.T) {
	l := &failFastLatch{}
	l.record(escp.ErrNoReply, writeRequest)
	l.record(escp.ErrNotFound, readRequest)
	err := l.refused()
	switch {
	case err == nil:
		t.Fatal("a latch that saw silence refuses nothing")
	case !errors.Is(err, escp.ErrNoReply) || errors.Is(err, escp.ErrNotFound):
		t.Errorf("got %v, want the kind of the first failure (ErrNoReply) and no other", err)
	case !strings.Contains(err.Error(), "a write") || strings.Contains(err.Error(), "a read"):
		t.Errorf("got %v, want it to name the first request, a write", err)
	}
}
