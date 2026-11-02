package regression

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// deferredTB is a test double whose cleanups, failure state and log the test controls, so the
// session's end-of-test behavior can be driven without failing the real test.
type deferredTB struct {
	testing.TB
	failed   bool
	logs     []string
	cleanups []func()
}

func (d *deferredTB) Cleanup(f func()) { d.cleanups = append(d.cleanups, f) }
func (d *deferredTB) Failed() bool     { return d.failed }
func (d *deferredTB) Log(args ...any)  { d.logs = append(d.logs, fmt.Sprint(args...)) }

// finish runs the cleanups last-registered first, as testing does.
func (d *deferredTB) finish() {
	for i := len(d.cleanups) - 1; i >= 0; i-- {
		d.cleanups[i]()
	}
}

func TestFailedScenarioPrintsWhatTheLibraryLogged(t *testing.T) {
	d := &deferredTB{TB: t}
	s := Open(d, noEnv)
	if err := s.Client.SetLED(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	d.failed = true
	d.finish()
	if len(d.logs) != 1 {
		t.Fatalf("%d log entries, want the one trace: %q", len(d.logs), d.logs)
	}
	got := d.logs[0]
	for _, want := range []string{"ESCP trace of the scenario", "component=escp", "msg=\"request sent\"", "operation=SetLED"} {
		if !strings.Contains(got, want) {
			t.Errorf("trace lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, s.Target.Password.Reveal()) || strings.Contains(got, s.Target.Username) {
		t.Errorf("the trace carries a credential:\n%s", got)
	}
}

func TestPassingScenarioStaysQuiet(t *testing.T) {
	d := &deferredTB{TB: t}
	s := Open(d, noEnv)
	if _, err := s.Client.LED(context.Background()); err != nil {
		t.Fatal(err)
	}
	d.finish()
	if len(d.logs) != 0 {
		t.Errorf("a passing scenario logged %q", d.logs)
	}
}

func TestTraceWithoutLinesPrintsNothing(t *testing.T) {
	d := &deferredTB{TB: t, failed: true}
	(&trace{}).dumpIfFailed(d)
	if len(d.logs) != 0 {
		t.Errorf("an empty trace logged %q", d.logs)
	}
}

func TestTraceKeepsTheLastLinesAndSaysHowManyWereDropped(t *testing.T) {
	r := &trace{}
	for i := range maxTraceLines + 3 {
		if _, err := fmt.Fprintf(r, "line %d\n", i); err != nil {
			t.Fatal(err)
		}
	}
	d := &deferredTB{TB: t, failed: true}
	r.dumpIfFailed(d)
	got := d.logs[0]
	if !strings.Contains(got, "the first 3 lines were dropped") || strings.Contains(got, "line 2\n") || !strings.Contains(got, "line 3") || !strings.HasSuffix(got, fmt.Sprintf("line %d", maxTraceLines+2)) {
		t.Errorf("trace = %.200s ... %.60s", got, got[max(0, len(got)-60):])
	}
	if n := strings.Count(got, "\n  line "); n != maxTraceLines {
		t.Errorf("%d lines kept, want %d", n, maxTraceLines)
	}
}
