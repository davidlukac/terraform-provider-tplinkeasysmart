package regression

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/escp-go"
)

// stubReader answers the reads of a script: each entry is how long the read takes (on the fake clock)
// and the error it returns; the last entry repeats.
type stubReader struct {
	clk    *fakeClock
	script []stubRead
	asked  int
}

type stubRead struct {
	takes time.Duration
	err   error
}

func (s *stubReader) LED(context.Context) (bool, error) {
	r := s.script[min(s.asked, len(s.script)-1)]
	s.asked++
	s.clk.t = s.clk.t.Add(r.takes)
	return true, r.err
}

// fakeClock is a clock whose time moves only when a read takes time or the wait sleeps.
type fakeClock struct{ t time.Time }

func (f *fakeClock) clock() clock {
	return clock{now: func() time.Time { return f.t }, sleep: func(d time.Duration) { f.t = f.t.Add(d) }}
}

func TestSettle(t *testing.T) {
	fast := stubRead{takes: 100 * time.Millisecond}
	slow := stubRead{takes: 4 * time.Second}
	broken := stubRead{takes: 10 * time.Second, err: escp.ErrNoReply}
	refused := stubRead{takes: 50 * time.Millisecond, err: &escp.RejectedError{Code: 1}}
	for name, tc := range map[string]struct {
		script  []stubRead
		asked   int
		waited  time.Duration
		wantErr string
	}{
		"a quiet switch is asked three times and not waited for":       {[]stubRead{fast}, 3, 300 * time.Millisecond, ""},
		"a slow read starts the count again":                           {[]stubRead{fast, fast, slow, fast}, 6, 2*100*time.Millisecond + 4*time.Second + time.Second + 3*100*time.Millisecond, ""},
		"a refusal is an answer":                                       {[]stubRead{refused}, 3, 150 * time.Millisecond, ""},
		"a failed read starts the count again and is paused after":     {[]stubRead{broken, fast}, 4, 10*time.Second + time.Second + 3*100*time.Millisecond, ""},
		"a switch that never settles is given up with the last reason": {[]stubRead{broken}, 17, 186 * time.Second, "the read got no reply after 10s"},
		"a switch that stays slow is given up with the last reason":    {[]stubRead{slow}, 37, 184 * time.Second, "the read took 4s"},
	} {
		t.Run(name, func(t *testing.T) {
			clk := &fakeClock{t: time.Unix(0, 0)}
			c := &stubReader{clk: clk, script: tc.script}
			waited, err := settle(context.Background(), c, clk.clock())
			if (err == nil) != (tc.wantErr == "") || (err != nil && !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if c.asked != tc.asked || waited != tc.waited {
				t.Errorf("asked %d times and waited %v, want %d and %v", c.asked, waited, tc.asked, tc.waited)
			}
		})
	}
}

func TestSettleStopsWhenTheContextEnds(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &stubReader{clk: clk, script: []stubRead{{takes: time.Millisecond}}}
	if _, err := settle(ctx, c, clk.clock()); !errors.Is(err, context.Canceled) || c.asked != 0 {
		t.Fatalf("err = %v after %d reads, want context.Canceled and no read", err, c.asked)
	}
}

func TestWaitUntilReadyFailsTheScenarioOfASwitchThatStaysSilent(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	ft := &fatalRecorder{TB: t}
	func() {
		defer func() { _ = recover() }() // the recorder stops the goroutine like a real Fatalf
		waitUntilReady(ft, "sw03", &stubReader{clk: clk, script: []stubRead{{takes: 10 * time.Second, err: escp.ErrNoReply}}}, clk.clock())
	}()
	if !ft.failed || !strings.Contains(ft.msg, "sw03 is not ready for a scenario") {
		t.Errorf("failed=%t msg=%q, want the scenario to fail naming the switch", ft.failed, ft.msg)
	}
}

func TestWaitUntilReadyPassesAndSaysWhenItHadToWait(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	waitUntilReady(t, "sw03", &stubReader{clk: clk, script: []stubRead{{takes: 5 * time.Second}, {takes: 10 * time.Millisecond}}}, clk.clock())
}
