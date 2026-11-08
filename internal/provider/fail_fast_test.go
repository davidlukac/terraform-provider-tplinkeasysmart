package provider

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
	"github.com/davidlukac/escp-go/model"
)

// callEveryRequest calls every request method of c (all of SwitchClient but Close and SwitchMAC)
// with zero arguments and returns the error of each, by method name.
func callEveryRequest(t *testing.T, c SwitchClient) map[string]error {
	t.Helper()
	out := map[string]error{}
	v := reflect.ValueOf(c)
	typ := reflect.TypeOf((*SwitchClient)(nil)).Elem()
	for i := range typ.NumMethod() {
		m := typ.Method(i)
		if m.Name == "Close" || m.Name == "SwitchMAC" {
			continue
		}
		args := make([]reflect.Value, 0, m.Type.NumIn())
		for j := range m.Type.NumIn() {
			if j == 0 {
				args = append(args, reflect.ValueOf(context.Background()))
				continue
			}
			args = append(args, reflect.Zero(m.Type.In(j)))
		}
		out[m.Name] = callOne(v.MethodByName(m.Name), args)
	}
	return out
}

// callOne calls a request method and returns its error. The in-memory fake panics on a setting it
// was not given (a nil table); that the request reached it is all the pass-through test needs, so
// a panic is reported as an error.
func callOne(method reflect.Value, args []reflect.Value) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the client panicked: %v", r)
		}
	}()
	res := method.Call(args)
	err, _ = res[len(res)-1].Interface().(error)
	return err
}

// A switch that does not answer is asked once: the first request takes the timeout, and every
// later request of every kind fails at once with that error, and sends nothing.
func TestFailFastStopsAskingASwitchThatDidNotAnswer(t *testing.T) {
	sw := escptest.Start(t)
	sw.Mute()
	c, err := escp.Dial(escp.Config{Host: "127.0.0.1", Username: escptest.Username, Password: model.Password(escptest.Password), Timeout: 60 * time.Millisecond,
		Retries: escp.NoRetries, SwitchPort: sw.UDPPort(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: escptest.Loopback()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	fast := failFast(c)
	if _, err := fast.Info(context.Background()); !errors.Is(err, escp.ErrNoReply) {
		t.Fatalf("the first request: got %v, want ErrNoReply", err)
	}
	sent := sw.Received()
	if sent == 0 {
		t.Fatal("the first request never reached the switch: this proves nothing")
	}
	for name, err := range callEveryRequest(t, fast) {
		if err == nil || !strings.Contains(err.Error(), "did not answer earlier in this run") || !errors.Is(err, escp.ErrNoReply) {
			t.Errorf("%s: got %v, want the latched error", name, err)
		}
	}
	if got := sw.Received(); got != sent {
		t.Fatalf("%d datagram(s) went to a switch that did not answer", got-sent)
	}
}

// A switch that answered once is never latched: a single request it does not answer says nothing
// about the next one (some firmware leaves some reads unanswered).
func TestFailFastNeverLatchesASwitchThatAnswered(t *testing.T) {
	fc := newFaulty(&fakeClient{})
	fast := failFast(fc)
	if _, err := fast.LED(context.Background()); err != nil {
		t.Fatal(err)
	}
	noReply := errors.Join(escp.ErrNoReply, errors.New("one read"))
	mustAs[latchedClient](t, fast).latch.record(noReply)
	if _, err := fast.LED(context.Background()); err != nil {
		t.Fatalf("a switch that has answered must not be latched: %v", err)
	}
}

// Every kind of outcome is classified: answers (and refusals) count as answers, silence and a switch
// that cannot be found latch while nothing answered, anything else (a canceled call) neither.
func TestFailFastClassifiesWhatARequestEnds(t *testing.T) {
	for _, tt := range []struct {
		name         string
		err          error
		wantAnswered bool
		wantLatched  bool
	}{
		{"an answer", nil, true, false},
		{"a refusal", &escp.RejectedError{Code: 5}, true, false},
		{"another device", &escp.WrongSwitchError{}, true, false},
		{"no reply", escp.ErrNoReply, false, true},
		{"a switch that cannot be found", escp.ErrNotFound, false, true},
		{"a canceled call", context.Canceled, false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			l := &failFastLatch{}
			l.record(tt.err)
			if l.answered != tt.wantAnswered || (l.err != nil) != tt.wantLatched {
				t.Fatalf("answered=%v latched=%v, want %v and %v", l.answered, l.err != nil, tt.wantAnswered, tt.wantLatched)
			}
		})
	}
}

// Without a failure the wrapper passes every request through: none is refused by the latch.
func TestFailFastPassesEveryRequestThrough(t *testing.T) {
	for name, err := range callEveryRequest(t, failFast(newFaulty(&fakeClient{}))) {
		if err != nil && strings.Contains(err.Error(), "did not answer earlier") {
			t.Errorf("%s was refused by the latch although nothing failed: %v", name, err)
		}
	}
}
