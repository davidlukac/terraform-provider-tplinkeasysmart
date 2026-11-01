package regression

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidlukac/escp-go"
)

// leaveDirty does what a scenario that failed halfway leaves behind: the VLAN mode switched and a
// mirror switched on, written straight through the library.
func leaveDirty(t *testing.T, s *Session) {
	t.Helper()
	ctx := context.Background()
	if err := s.Client.SetVLANMode(ctx, escp.VLANModeSetting{Mode: escp.VLANModeDot1Q}); err != nil {
		t.Fatal(err)
	}
	mirror := escp.Mirror{Enabled: true, MirrorPort: 3, IngressPorts: escp.MustPortSet(4)}
	if err := s.Client.SetPortMirror(ctx, mirror); err != nil {
		t.Fatal(err)
	}
}

func TestRescuePutsBackTheVLANModeAndTheMirror(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	leaveDirty(t, s)
	if diff, err := s.Restored(ctx); err != nil || len(diff) == 0 {
		t.Fatalf("the setup left the switch as it was: diff %q, err %v", diff, err)
	}
	done, err := s.Rescue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 || !strings.HasPrefix(done[0], "VLAN mode") || !strings.HasPrefix(done[1], "port mirror") {
		t.Errorf("rescued %q, want the VLAN mode then the port mirror", done)
	}
	if diff, err := s.Restored(ctx); err != nil || len(diff) != 0 {
		t.Errorf("after the rescue: diff %q, err %v, want the opening readout", diff, err)
	}
}

func TestRescueWritesNothingWhenTheSwitchIsAsItWas(t *testing.T) {
	s := Open(t, noEnv)
	writes := s.Fake.WriteCount()
	done, err := s.Rescue(context.Background())
	if err != nil || len(done) != 0 {
		t.Fatalf("rescued %q, err %v, want nothing", done, err)
	}
	if got := s.Fake.WriteCount(); got != writes {
		t.Errorf("the safety net wrote %d times to a switch that was as it was", got-writes)
	}
}

func TestRescueRestoresOnlyWhatDiffers(t *testing.T) {
	s := Open(t, noEnv)
	mirror := escp.Mirror{Enabled: true, MirrorPort: 3, IngressPorts: escp.MustPortSet(4)}
	if err := s.Client.SetPortMirror(context.Background(), mirror); err != nil {
		t.Fatal(err)
	}
	done, err := s.Rescue(context.Background())
	if err != nil || len(done) != 1 || !strings.HasPrefix(done[0], "port mirror") {
		t.Errorf("rescued %q, err %v, want only the port mirror", done, err)
	}
}

func TestGuardRestoreFailsTheScenarioThatNeededTheNet(t *testing.T) {
	s := Open(t, noEnv)
	leaveDirty(t, s)
	rec := &errorRecorder{TB: t}
	s.rescueAtEnd(rec)
	if len(rec.errors) != 2 || !strings.Contains(rec.errors[0], "safety net put back the VLAN mode") || !strings.Contains(rec.errors[1], "port mirror") {
		t.Errorf("errors = %q, want one per setting put back", rec.errors)
	}
	if diff, err := s.Restored(context.Background()); err != nil || len(diff) != 0 {
		t.Errorf("after the net: diff %q, err %v", diff, err)
	}
	// A scenario that cleaned up after itself is not reported.
	clean := &errorRecorder{TB: t}
	s.rescueAtEnd(clean)
	if len(clean.errors) != 0 {
		t.Errorf("errors = %q on a switch that is as it was", clean.errors)
	}
}

// TestGuardRestoreRunsWhenTheTestEnds proves the registration: the net runs as a cleanup of the
// test it was given, and a test that left the switch changed is failed by it.
func TestGuardRestoreRunsWhenTheTestEnds(t *testing.T) {
	s := Open(t, noEnv)
	rec := &cleanupRecorder{errorRecorder: errorRecorder{TB: t}}
	s.GuardRestore(rec)
	leaveDirty(t, s)
	rec.runCleanups()
	if len(rec.errors) != 2 {
		t.Errorf("errors = %q, want the two settings reported when the test ends", rec.errors)
	}
}

type cleanupRecorder struct {
	errorRecorder
	cleanups []func()
}

func (c *cleanupRecorder) Cleanup(f func()) { c.cleanups = append(c.cleanups, f) }
func (c *cleanupRecorder) runCleanups() {
	for i := len(c.cleanups) - 1; i >= 0; i-- {
		c.cleanups[i]()
	}
}

// stubRescue is a client whose reads and writes fail on demand, for the paths a fake cannot reach
// without scripting every reply.
type stubRescue struct {
	mode   escp.VLANModeSetting
	mirror escp.Mirror
	// readModeErr, setModeErr, readMirrorErr and setMirrorErr make the matching call fail.
	readModeErr, setModeErr, readMirrorErr, setMirrorErr error
}

func (c *stubRescue) VLANMode(context.Context) (escp.VLANModeSetting, error) {
	return c.mode, c.readModeErr
}
func (c *stubRescue) SetVLANMode(_ context.Context, m escp.VLANModeSetting) error {
	if c.setModeErr == nil {
		c.mode = m
	}
	return c.setModeErr
}
func (c *stubRescue) PortMirror(context.Context) (escp.Mirror, error) {
	return c.mirror, c.readMirrorErr
}
func (c *stubRescue) SetPortMirror(_ context.Context, m escp.Mirror) error {
	if c.setMirrorErr == nil {
		c.mirror = m
	}
	return c.setMirrorErr
}

func TestRescueReportsWhatItCannotDo(t *testing.T) {
	boom := errors.New("boom")
	dirtyMirror := escp.Mirror{Enabled: true, MirrorPort: 3, IngressPorts: escp.MustPortSet(4)}
	for _, c := range []struct {
		name string
		stub stubRescue
		want string // in the error
		done int    // settings put back before it failed
	}{
		{"the VLAN mode cannot be read", stubRescue{readModeErr: boom}, "reading the VLAN mode", 0},
		{"the VLAN mode cannot be written", stubRescue{mode: escp.VLANModeSetting{Mode: escp.VLANModeDot1Q}, setModeErr: boom}, "putting the VLAN mode back", 0},
		{"the mirror cannot be read", stubRescue{readMirrorErr: boom}, "reading the port mirror", 0},
		{"the mirror cannot be written", stubRescue{mirror: dirtyMirror, setMirrorErr: boom}, "putting the port mirror back", 0},
		{"the mode went back but the mirror cannot be read", stubRescue{mode: escp.VLANModeSetting{Mode: escp.VLANModeDot1Q}, readMirrorErr: boom}, "reading the port mirror", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			opening := escp.VLANModeSetting{Mode: escp.VLANModePortBased}
			if c.stub.mode == (escp.VLANModeSetting{}) {
				c.stub.mode = opening // a case about something else starts with the mode as it was
			}
			s := &Session{net: &c.stub, mode: opening}
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

func TestRescueAtEndNamesWhatItCouldNotFinish(t *testing.T) {
	s := &Session{net: &stubRescue{readModeErr: errors.New("boom")}}
	rec := &errorRecorder{TB: t}
	s.rescueAtEnd(rec)
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "could not finish") {
		t.Errorf("errors = %q", rec.errors)
	}
}

func TestCaptureReportsWhatItCannotRead(t *testing.T) {
	boom := errors.New("boom")
	if err := (&Session{net: &stubRescue{readModeErr: boom}}).capture(context.Background()); !errors.Is(err, boom) || !strings.Contains(err.Error(), "VLAN mode") {
		t.Errorf("err = %v, want the VLAN mode named", err)
	}
	if err := (&Session{net: &stubRescue{readMirrorErr: boom}}).capture(context.Background()); !errors.Is(err, boom) || !strings.Contains(err.Error(), "port mirror") {
		t.Errorf("err = %v, want the port mirror named", err)
	}
}

func TestRescueCannotWriteAnOpeningModeThatIsNone(t *testing.T) {
	s := Open(t, noEnv)
	s.mode = escp.VLANModeSetting{Mode: escp.VLANModeNone} // a switch that began with no single mode
	if _, err := s.Rescue(context.Background()); err == nil || !strings.Contains(err.Error(), "putting the VLAN mode back") {
		t.Errorf("err = %v, want the unwritable opening mode named", err)
	}
}
