package escp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestExpiredReportsCancellationAndPassedDeadlines(t *testing.T) {
	if err := expired(context.Background()); err != nil {
		t.Fatalf("a context without a deadline is not expired: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	if err := expired(ctx); err != nil {
		t.Fatalf("a deadline in the future is not expired: %v", err)
	}
	cancel()
	if err := expired(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled context must report cancellation, got %v", err)
	}
}

// The deadline can pass before the context's own timer has marked it done.
// expired must still report it, which is what makes the callers stop retrying.
func TestExpiredNoticesADeadlineThatThePassedTimerHasNotReportedYet(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	if err := expired(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a deadline in the past must be reported as exceeded, got %v", err)
	}
}

// deadlineOnlyContext reports a deadline in the past while Err is still nil,
// the window in which the socket deadline has fired but the context's own timer
// has not. That window is real but microscopic, so it is built by hand to cover
// the branch deterministically instead of by timing.
type deadlineOnlyContext struct{ context.Context }

func (deadlineOnlyContext) Deadline() (time.Time, bool) {
	return time.Now().Add(-time.Second), true
}

func (deadlineOnlyContext) Err() error { return nil }

func TestExpiredComparesTheDeadlineWhenTheContextHasNotNoticedYet(t *testing.T) {
	err := expired(deadlineOnlyContext{context.Background()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a passed deadline with a nil Err must be reported as exceeded, got %v", err)
	}
}
