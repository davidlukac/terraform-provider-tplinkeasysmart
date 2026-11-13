package nosleeptests

import (
	"testing"
	"time"
)

func TestSleepsAreReportedInTests(t *testing.T) {
	time.Sleep(time.Millisecond) // want `time.Sleep waits for a guess`
}

// A select case that fails the test is the bound of a wait that has a signal: not reported.
func TestTheFailureBoundOfAWait(t *testing.T) {
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("it did not end")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Errorf("it did not end in %v", time.Second)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		panic("it did not end")
	}
	select {
	case <-done:
	case _ = <-time.After(time.Second):
		if testing.Short() {
			t.Skip("too slow")
		}
		t.FailNow()
	}
}

func bounded(tb testing.TB, done chan struct{}) {
	tb.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		tb.Fatalf("it did not end")
	}
}

// The expected end of a wait, a case that does not fail, and a bare wait are guesses.
func TestGuesses(t *testing.T) {
	done := make(chan struct{})
	select {
	case <-done:
		t.Fatal("it ended, and it must not have")
	case <-time.After(300 * time.Millisecond): // want `time.After in a test that is not the failure bound`
	}
	select {
	case <-done:
	case <-time.After(time.Second): // want `time.After in a test that is not the failure bound`
		close(done)
	}
	<-time.After(time.Millisecond)     // want `time.After in a test that is not the failure bound`
	timeout := time.After(time.Second) // want `time.After in a test that is not the failure bound`
	<-timeout
}

// A case whose body only looks like a failure: a method of another type with the same name is not one.
type logger struct{}

func (logger) Fatal(...any) {}

func TestAMethodOfAnotherType(t *testing.T) {
	var l logger
	done := make(chan struct{})
	select {
	case <-done:
	case <-time.After(time.Second): // want `time.After in a test that is not the failure bound`
		l.Fatal("not a testing value")
	}
}

// An intended wait on the clock carries its reason.
func TestAnnotated(t *testing.T) {
	done := make(chan struct{})
	select {
	case <-done:
		t.Fatal("it ended")
	case <-time.After(10 * time.Millisecond): // nosleep: the wait is the thing under test, a real window of the fake
	}
	// nosleep: a read deadline of the real socket is the behavior under test
	time.Sleep(time.Millisecond)
}

// nosleep: the whole function paces a real socket.
func TestAnnotatedByTheDocComment(t *testing.T) {
	time.Sleep(time.Millisecond)
	<-time.After(time.Millisecond)
}

// A failure inside an if, or a method of time.Time that is called After, does not make a case a bound.
func TestIncidentalFailuresAreNotBounds(t *testing.T) {
	done := make(chan struct{})
	second := make(chan error, 1)
	select {
	case <-done:
	case <-time.After(300 * time.Millisecond): // want `time.After in a test that is not the failure bound`
		close(done)
		if err := <-second; err != nil {
			t.Fatal(err)
		}
	}
	if time.Now().After(time.Now()) { // the method of time.Time, not a wait
		t.Log("after")
	}
}
