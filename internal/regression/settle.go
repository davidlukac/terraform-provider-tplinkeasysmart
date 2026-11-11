package regression

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/davidlukac/escp-go"
)

// A switch that has just had its device ports taken down and up (the disruptive scenarios) stays slow
// for a while: its management CPU answers discovery but not reads, for ten seconds or more, and the
// scenario that follows fails on a read that timed out although nothing is wrong with the code under
// test (seen twice on switch 03, in the MAC-addressed scenarios that run right after the disruptive
// ones). A scenario therefore starts only when the switch answers reads promptly again.
const (
	// settleReplies is how many reads in a row must be answered promptly.
	settleReplies = 3
	// settleFast is the longest a prompt answer may take; a real switch answers a read in 100 to 300 ms.
	settleFast = time.Second
	// settleRead bounds one read, so a silent switch is retried and not waited on for the retries of the client.
	settleRead = 10 * time.Second
	// settlePause is the pause after a read that was slow or failed.
	settlePause = time.Second
	// settleBudget is how long the switch gets before the scenario is given up as not runnable.
	settleBudget = 3 * time.Minute
)

// settleClient is what the wait needs of a client: one authenticated read that is cheap for the switch.
type settleClient interface {
	LED(ctx context.Context) (bool, error)
}

// clock is time, replaceable in tests.
type clock struct {
	now   func() time.Time
	sleep func(time.Duration)
}

var wallClock = clock{now: time.Now, sleep: time.Sleep}

// waitUntilReady holds a scenario back until the switch answers reads promptly, and fails it when the
// switch does not within the budget. What it waited is logged (visible on failure and with -v).
func waitUntilReady(tb testing.TB, name string, c settleClient, clk clock) {
	tb.Helper()
	waited, err := settle(context.Background(), c, clk)
	if err != nil {
		tb.Fatalf("%s is not ready for a scenario: %v", name, err)
	}
	if waited > settleFast*settleReplies {
		tb.Logf("waited %v for %s to answer reads promptly", waited.Round(time.Second), name)
	}
}

// settle reads the LED until settleReplies reads in a row are answered within settleFast, and returns
// how long that took. A read that gets no reply, or a slow one, starts the count again. It gives up when
// settleBudget has passed or ctx ends, and says what the last read did.
func settle(ctx context.Context, c settleClient, clk clock) (time.Duration, error) {
	start := clk.now()
	prompt := 0
	var last string // what the latest read that was not prompt did
	for {
		if err := ctx.Err(); err != nil {
			return clk.now().Sub(start), err
		}
		readCtx, cancel := context.WithTimeout(ctx, settleRead)
		began := clk.now()
		_, err := c.LED(readCtx)
		cancel()
		took := clk.now().Sub(began)
		// A refusal (the switch said no) is an answer, and the scenario reports it in its own words; what
		// is waited out is silence, a read that gets no reply.
		silent := errors.Is(err, escp.ErrNoReply) || errors.Is(err, context.DeadlineExceeded)
		switch {
		case !silent && took <= settleFast:
			if prompt++; prompt >= settleReplies {
				return clk.now().Sub(start), nil
			}
			continue
		case silent:
			last = fmt.Sprintf("the read got no reply after %v: %v", took.Round(time.Millisecond), err)
		default:
			last = fmt.Sprintf("the read took %v", took.Round(time.Millisecond))
		}
		prompt = 0
		if waited := clk.now().Sub(start); waited >= settleBudget {
			return waited, fmt.Errorf("the switch did not answer %d reads in a row within %v after %v: %s", settleReplies, settleFast, waited.Round(time.Second), last)
		}
		clk.sleep(settlePause)
	}
}
