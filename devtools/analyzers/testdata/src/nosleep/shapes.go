package nosleep

import "time"

// pollWithASleep is production code that waits for a guess: reported, in production as in a test.
func pollWithASleep(ready func() bool) {
	for !ready() {
		time.Sleep(time.Millisecond) // want `time.Sleep waits for a guess`
	}
}

// pause is the plain form.
func pause() {
	time.Sleep(time.Second) // want `time.Sleep waits for a guess`
}

// inAClosure is found inside a function literal and in a variable's initializer too.
var inAnInitializer = func() { time.Sleep(time.Second) } // want `time.Sleep waits for a guess`

func inAClosure() {
	go func() {
		time.Sleep(time.Second) // want `time.Sleep waits for a guess`
	}()
}

// onTheLine explains the wait on its own line.
func onTheLine() {
	time.Sleep(time.Second) // nosleep: the pause is the rate limit of the protocol, not a guess
}

// onTheLineAbove explains the wait on the line above it.
func onTheLineAbove() {
	// nosleep: the pause is the rate limit of the protocol, not a guess
	time.Sleep(time.Second)
}

// inTheDocComment explains every wait of the function. nosleep: a real-time pacing loop.
func inTheDocComment() {
	time.Sleep(time.Second)
	time.Sleep(time.Second)
}

// anotherFunctionIsNotExcused shows that the doc comment above covers only its own function.
func anotherFunctionIsNotExcused() {
	time.Sleep(time.Second) // want `time.Sleep waits for a guess`
}

// withoutAReason carries the marker and no reason: the marker is reported, once, and the call stays
// explained (the author is told to write the reason, not to find the place again).
func withoutAReason() {
	/* nosleep: */ // want `a nosleep annotation needs a reason`
	time.Sleep(time.Second)
}

// timers are not a sleep as far as the check goes: it only knows time.Sleep in production code, and
// production code has its own rules for the others.
func timers() {
	t := time.NewTimer(time.Second)
	<-t.C
	<-time.After(time.Second)
}

// notThePackage is a Sleep that is not time.Sleep.
type clock struct{}

func (clock) Sleep(time.Duration) {}

func aMethodNamedSleep(c clock) {
	c.Sleep(time.Second)
}
