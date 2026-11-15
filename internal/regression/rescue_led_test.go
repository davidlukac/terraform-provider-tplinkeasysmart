package regression

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidlukac/escp-go/model"
)

// TestRescueRestoresTheLED covers the third setting of the safety net: the LED, recorded when the
// session opened and written back as that recorded value, whatever the switch holds at the end.
func TestRescueRestoresTheLED(t *testing.T) {
	for _, c := range []struct {
		name      string
		opening   bool // the LED when the session opened
		atTheEnd  bool // the LED when the scenario is over
		wantLine  string
		wantWrite int
	}{
		{"left off, was on", true, false, "LED true, was left at false", 1},
		{"left on, was off", false, true, "LED false, was left at true", 1},
		{"on as it was", true, true, "", 0},
		{"off as it was", false, false, "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := &stubRescue{led: c.atTheEnd}
			s := &Session{net: stub, led: c.opening}
			done, err := s.Rescue(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if stub.led != c.opening {
				t.Errorf("LED on the switch = %t, want the recorded %t", stub.led, c.opening)
			}
			if stub.ledWrites != c.wantWrite {
				t.Errorf("LED writes = %d, want %d", stub.ledWrites, c.wantWrite)
			}
			switch {
			case c.wantLine == "" && len(done) != 0:
				t.Errorf("rescued %q, want nothing", done)
			case c.wantLine != "" && (len(done) != 1 || done[0] != c.wantLine):
				t.Errorf("rescued %q, want the one line %q", done, c.wantLine)
			}
		})
	}
}

func TestRescueReportsWhatItCannotDoWithTheLED(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		stub stubRescue
		want string
		done int // settings put back before it failed
	}{
		{"the LED cannot be read", stubRescue{readLEDErr: boom}, "reading the LED", 0},
		{"the LED cannot be written", stubRescue{led: false, setLEDErr: boom}, "putting the LED back", 0},
		{"the mode went back but the LED cannot be read", stubRescue{mode: model.VLANModeSetting{Mode: model.VLANModeDot1Q}, readLEDErr: boom}, "reading the LED", 1},
		{"the mirror went back but the LED cannot be written", stubRescue{mirror: model.Mirror{Enabled: true, Destination: 3, IngressPorts: model.MustPortSet(4)}, setLEDErr: boom}, "putting the LED back", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			opening := model.VLANModeSetting{Mode: model.VLANModePortBased}
			if c.stub.mode == (model.VLANModeSetting{}) {
				c.stub.mode = opening
			}
			// The LED was on when the session opened; the stub holds it off, so the write is attempted.
			s := &Session{net: &c.stub, mode: opening, led: true}
			done, err := s.Rescue(context.Background())
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q wrapping the cause", err, c.want)
			}
			if len(done) != c.done {
				t.Errorf("done = %q, want %d lines", done, c.done)
			}
		})
	}
}

func TestCaptureReportsWhatItCannotReadOfTheLED(t *testing.T) {
	boom := errors.New("boom")
	err := (&Session{net: &stubRescue{readLEDErr: boom}}).capture(context.Background())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "LED") {
		t.Errorf("err = %v, want the LED named", err)
	}
}

func TestCaptureRecordsTheLED(t *testing.T) {
	for _, on := range []bool{true, false} {
		name := "off"
		if on {
			name = "on"
		}
		t.Run(name, func(t *testing.T) {
			s := &Session{net: &stubRescue{led: on}, led: !on}
			if err := s.capture(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s.led != on {
				t.Errorf("recorded LED = %t, want %t", s.led, on)
			}
		})
	}
}

// TestRescueOrderIsModeMirrorLED fixes the order of the lines when all three settings were
// changed: the mode first (it discards the settings of the other modes), the LED last.
func TestRescueOrderIsModeMirrorLED(t *testing.T) {
	s := Open(t, noEnv)
	leaveDirty(t, s)
	if err := s.Client().SetLED(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	done, err := s.Rescue(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 3 || !strings.HasPrefix(done[0], "VLAN mode") || !strings.HasPrefix(done[1], "port mirror") || !strings.HasPrefix(done[2], "LED") {
		t.Errorf("rescued %q, want the VLAN mode, the port mirror, then the LED", done)
	}
}

// TestGuardRestorePutsBackTheLEDAFailedScenarioLeftOff is the scenario of the hardware runs, on
// the fake: a scenario turns the LED off and fails before its own cleanup. The net registered by
// GuardRestore puts the LED back when the test ends, fails the scenario for it, and the opening
// readout matches again, so the next scenario does not inherit an LED that is off.
func TestGuardRestorePutsBackTheLEDAFailedScenarioLeftOff(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	if on, err := s.Client().LED(ctx); err != nil || !on {
		t.Fatalf("the fake starts with the LED on: %t, err %v", on, err)
	}
	rec := &cleanupRecorder{TB: t}
	s.GuardRestore(rec)
	if err := s.Client().SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	// The scenario fails here, before the step that would have put the LED back.
	rec.runCleanups()
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "safety net put back the LED") {
		t.Errorf("errors = %q, want the scenario failed for the LED", rec.errors)
	}
	if on, err := s.Client().LED(ctx); err != nil || !on {
		t.Errorf("LED after the net = %t, err %v, want the recorded on", on, err)
	}
	if diff, err := s.Restored(ctx); err != nil || len(diff) != 0 {
		t.Errorf("after the net: diff %q, err %v, want the opening readout", diff, err)
	}
}

// TestRescueSettingsAreIndependent proves that a restore that fails never keeps the next one from
// running: the net is the last chance to leave the switch as it was found, and the LED is the
// setting a failed scenario most often leaves behind. Every error is joined and names its setting,
// and the lines of the changes that were made are returned with them.
func TestRescueSettingsAreIndependent(t *testing.T) {
	boom, bang := errors.New("boom"), errors.New("bang")
	dirtyMirror := model.Mirror{Enabled: true, Destination: 3, IngressPorts: model.MustPortSet(4)}
	opening := model.VLANModeSetting{Mode: model.VLANModePortBased}
	dot1q := model.VLANModeSetting{Mode: model.VLANModeDot1Q}
	for _, c := range []struct {
		name      string
		stub      stubRescue
		wantErrs  []error  // each must be in the joined error
		wantText  []string // fragments the joined error must name
		wantLines []string // prefixes of the lines, in order
		wantLED   bool     // the LED on the stub afterwards (the recorded one is on)
	}{
		{
			name:      "the mode cannot be read, the mirror and the LED are put back",
			stub:      stubRescue{readModeErr: boom, mirror: dirtyMirror},
			wantErrs:  []error{boom},
			wantText:  []string{"reading the VLAN mode"},
			wantLines: []string{"port mirror", "LED"},
			wantLED:   true,
		},
		{
			name:      "the mode cannot be written, the mirror and the LED are put back",
			stub:      stubRescue{mode: dot1q, setModeErr: boom, mirror: dirtyMirror},
			wantErrs:  []error{boom},
			wantText:  []string{"putting the VLAN mode back"},
			wantLines: []string{"port mirror", "LED"},
			wantLED:   true,
		},
		{
			name:      "the mirror cannot be read, the mode went back and the LED is put back",
			stub:      stubRescue{mode: dot1q, readMirrorErr: boom},
			wantErrs:  []error{boom},
			wantText:  []string{"reading the port mirror"},
			wantLines: []string{"VLAN mode", "LED"},
			wantLED:   true,
		},
		{
			name:      "the mirror cannot be written, the mode went back and the LED is put back",
			stub:      stubRescue{mode: dot1q, mirror: dirtyMirror, setMirrorErr: boom},
			wantErrs:  []error{boom},
			wantText:  []string{"putting the port mirror back"},
			wantLines: []string{"VLAN mode", "LED"},
			wantLED:   true,
		},
		{
			name:      "two errors are both reported, with the line of what was put back",
			stub:      stubRescue{readModeErr: boom, mirror: dirtyMirror, setLEDErr: bang},
			wantErrs:  []error{boom, bang},
			wantText:  []string{"reading the VLAN mode", "putting the LED back"},
			wantLines: []string{"port mirror"},
			wantLED:   false, // the write failed
		},
		{
			name:     "every setting fails and each is named",
			stub:     stubRescue{readModeErr: boom, readMirrorErr: boom, readLEDErr: bang},
			wantErrs: []error{boom, bang},
			wantText: []string{"reading the VLAN mode", "reading the port mirror", "reading the LED"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.stub.mode == (model.VLANModeSetting{}) {
				c.stub.mode = opening
			}
			s := &Session{net: &c.stub, mode: opening, led: true} // the LED is off on the stub
			done, err := s.Rescue(context.Background())
			for _, want := range c.wantErrs {
				if !errors.Is(err, want) {
					t.Errorf("err = %v, want it to wrap %v", err, want)
				}
			}
			for _, text := range c.wantText {
				if err == nil || !strings.Contains(err.Error(), text) {
					t.Errorf("err = %v, want %q named", err, text)
				}
			}
			if len(done) != len(c.wantLines) {
				t.Fatalf("done = %q, want %d lines", done, len(c.wantLines))
			}
			for i, prefix := range c.wantLines {
				if !strings.HasPrefix(done[i], prefix) {
					t.Errorf("line %d = %q, want prefix %q", i, done[i], prefix)
				}
			}
			if c.stub.led != c.wantLED {
				t.Errorf("LED on the switch = %t, want %t", c.stub.led, c.wantLED)
			}
		})
	}
}
