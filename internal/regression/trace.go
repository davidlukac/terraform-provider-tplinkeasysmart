package regression

import (
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// maxTraceLines bounds the protocol trace kept for a scenario: the last lines are the ones that
// explain a failure, and a long scenario must not grow without limit.
const maxTraceLines = 4000

// trace is a flight recorder for the library's log: it keeps what the escp clients of a session
// say (at Debug, so every request and reply) and prints it only when the test fails, so a passing
// scenario stays quiet even under -v and a failing one shows the protocol of its last steps. It
// is an io.Writer for slog's text handler, which writes one line per record. Safe for concurrent
// use: the provider's clients and the suite's own talk from different goroutines.
type trace struct {
	mu      sync.Mutex
	lines   []string
	dropped int
}

// Write keeps one log line, dropping the oldest when the trace is full.
func (r *trace) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) == maxTraceLines {
		r.lines = r.lines[1:]
		r.dropped++
	}
	r.lines = append(r.lines, strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// logger is what Config.Logger of the session's clients is set to: the text handler at Debug.
func (r *trace) logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(r, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// dumpIfFailed prints the trace to the test log when the test has failed, and nothing otherwise.
// It is registered first so it runs last, after the clients are closed and the safety net has run.
func (r *trace) dumpIfFailed(tb testing.TB) {
	if !tb.Failed() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("ESCP trace of the scenario (what the library logged):")
	if r.dropped > 0 {
		b.WriteString(" the first ")
		b.WriteString(strconv.Itoa(r.dropped))
		b.WriteString(" lines were dropped")
	}
	for _, l := range r.lines {
		b.WriteString("\n  ")
		b.WriteString(l)
	}
	tb.Log(b.String())
}
