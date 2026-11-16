package regression

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/davidlukac/escp-go/model"
)

// igmpLoopStub is the part of stubRescue that holds IGMP snooping and loop prevention: the value
// each call reads, the error each call returns on demand, and the writes it counted.
type igmpLoopStub struct {
	igmp model.IGMP
	loop bool
	// readIGMPErr, setIGMPErr, readLoopErr and setLoopErr make the matching call fail.
	readIGMPErr, setIGMPErr, readLoopErr, setLoopErr error
	// igmpWrites and loopWrites count the writes, for the cases that must see none.
	igmpWrites, loopWrites int
	// written is the last IGMP configuration written.
	written model.IGMPConfig
}

func (c *igmpLoopStub) IGMP(context.Context) (model.IGMP, error) { return c.igmp, c.readIGMPErr }
func (c *igmpLoopStub) SetIGMP(_ context.Context, cfg model.IGMPConfig) error {
	c.igmpWrites++
	c.written = cfg
	if c.setIGMPErr == nil {
		c.igmp.Enabled, c.igmp.ReportSuppression = cfg.Enabled, cfg.ReportSuppression
	}
	return c.setIGMPErr
}
func (c *igmpLoopStub) LoopPrevention(context.Context) (bool, error) { return c.loop, c.readLoopErr }
func (c *igmpLoopStub) SetLoopPrevention(_ context.Context, on bool) error {
	c.loopWrites++
	if c.setLoopErr == nil {
		c.loop = on
	}
	return c.setLoopErr
}

// learnedGroup is a multicast group the switch learned at run time: data that is not a setting.
func learnedGroup(last byte) model.IGMPGroup {
	return model.IGMPGroup{IP: netip.AddrFrom4([4]byte{239, 255, 0, last}), VLAN: 1, Ports: model.MustPortSet(3)}
}

// wantOneLine fails the test unless the net reported the one line, or nothing when want is empty.
func wantOneLine(t *testing.T, done []string, want string) {
	t.Helper()
	switch {
	case want == "" && len(done) != 0:
		t.Errorf("rescued %q, want nothing", done)
	case want != "" && (len(done) != 1 || done[0] != want):
		t.Errorf("rescued %q, want the one line %q", done, want)
	}
}

func TestRescueRestoresIGMP(t *testing.T) {
	for _, c := range []struct {
		name      string
		opening   model.IGMPConfig
		atTheEnd  model.IGMP
		wantLine  string
		wantWrite int
	}{
		{"snooping left off, was on", igmpConfig(true, false), model.IGMP{Enabled: false, ReportSuppression: false}, "IGMP snooping {Enabled:true ReportSuppression:false}, was left at {Enabled:false ReportSuppression:false}", 1},
		{"suppression left on, was off", igmpConfig(true, false), model.IGMP{Enabled: true, ReportSuppression: true}, "IGMP snooping {Enabled:true ReportSuppression:false}, was left at {Enabled:true ReportSuppression:true}", 1},
		{"both differ", igmpConfig(false, true), model.IGMP{Enabled: true, ReportSuppression: false}, "IGMP snooping {Enabled:false ReportSuppression:true}, was left at {Enabled:true ReportSuppression:false}", 1},
		{"as it was", igmpConfig(true, true), model.IGMP{Enabled: true, ReportSuppression: true}, "", 0},
		{"off as it was", igmpConfig(false, false), model.IGMP{}, "", 0},
		{
			"as it was, with groups learned meanwhile", igmpConfig(true, false),
			model.IGMP{Enabled: true, ReportSuppression: false, Groups: []model.IGMPGroup{learnedGroup(1), learnedGroup(2)}}, "", 0,
		},
		{
			"differs, with groups learned meanwhile", igmpConfig(true, false),
			model.IGMP{Enabled: false, ReportSuppression: false, Groups: []model.IGMPGroup{learnedGroup(1)}},
			"IGMP snooping {Enabled:true ReportSuppression:false}, was left at {Enabled:false ReportSuppression:false}", 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := &stubRescue{igmp: c.atTheEnd}
			s := &Session{net: stub, igmp: c.opening}
			done, err := s.Rescue(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := igmpConfig(stub.igmp.Enabled, stub.igmp.ReportSuppression); got != c.opening {
				t.Errorf("IGMP on the switch = %+v, want the recorded %+v", got, c.opening)
			}
			if stub.igmpWrites != c.wantWrite {
				t.Errorf("IGMP writes = %d, want %d", stub.igmpWrites, c.wantWrite)
			}
			if c.wantWrite == 1 && stub.written != c.opening {
				t.Errorf("IGMP written = %+v, want the recorded %+v", stub.written, c.opening)
			}
			wantOneLine(t, done, c.wantLine)
		})
	}
}

func TestRescueRestoresLoopPrevention(t *testing.T) {
	for _, c := range []struct {
		name      string
		opening   bool
		atTheEnd  bool
		wantLine  string
		wantWrite int
	}{
		{"left off, was on", true, false, "loop prevention true, was left at false", 1},
		{"left on, was off", false, true, "loop prevention false, was left at true", 1},
		{"on as it was", true, true, "", 0},
		{"off as it was", false, false, "", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := &stubRescue{loop: c.atTheEnd}
			s := &Session{net: stub, loop: c.opening}
			done, err := s.Rescue(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if stub.loop != c.opening {
				t.Errorf("loop prevention on the switch = %t, want the recorded %t", stub.loop, c.opening)
			}
			if stub.loopWrites != c.wantWrite {
				t.Errorf("loop prevention writes = %d, want %d", stub.loopWrites, c.wantWrite)
			}
			wantOneLine(t, done, c.wantLine)
		})
	}
}

// dirtyStub is a stub whose five settings all differ from the session recorded by dirtySession,
// so that a restore is attempted for each of them.
func dirtyStub() *stubRescue {
	return &stubRescue{
		mode:   model.VLANModeSetting{Mode: model.VLANModeDot1Q},
		mirror: model.Mirror{Enabled: true, Destination: 3, IngressPorts: model.MustPortSet(4)},
		led:    false,
		igmp:   model.IGMP{Enabled: false, ReportSuppression: true},
	}
}

func dirtySession(stub *stubRescue) *Session {
	return &Session{net: stub, mode: model.VLANModeSetting{Mode: model.VLANModePortBased}, led: true, igmp: igmpConfig(true, false), loop: true}
}

// TestRescueSettingsAreIndependentOfIGMPAndLoopPrevention breaks one setting at a time of the five
// and shows that each of the other four is still put back, in order, and that the error names the
// setting that failed.
func TestRescueSettingsAreIndependentOfIGMPAndLoopPrevention(t *testing.T) {
	boom := errors.New("boom")
	lines := []string{"VLAN mode", "port mirror", "LED", "IGMP snooping", "loop prevention"}
	for _, c := range []struct {
		name   string
		break_ func(*stubRescue)
		failed int    // index in lines of the setting that fails
		want   string // in the error
	}{
		{"IGMP cannot be read", func(s *stubRescue) { s.readIGMPErr = boom }, 3, "reading the IGMP snooping"},
		{"IGMP cannot be written", func(s *stubRescue) { s.setIGMPErr = boom }, 3, "putting the IGMP snooping back"},
		{"loop prevention cannot be read", func(s *stubRescue) { s.readLoopErr = boom }, 4, "reading the loop prevention"},
		{"loop prevention cannot be written", func(s *stubRescue) { s.setLoopErr = boom }, 4, "putting the loop prevention back"},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := dirtyStub()
			c.break_(stub)
			done, err := dirtySession(stub).Rescue(context.Background())
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q wrapping the cause", err, c.want)
			}
			var want []string
			for i, l := range lines {
				if i != c.failed {
					want = append(want, l)
				}
			}
			if len(done) != len(want) {
				t.Fatalf("done = %q, want a line for each of %q", done, want)
			}
			for i, prefix := range want {
				if !strings.HasPrefix(done[i], prefix) {
					t.Errorf("line %d = %q, want prefix %q", i, done[i], prefix)
				}
			}
		})
	}
}

func TestRescueEarlierFailuresDoNotSkipIGMPAndLoopPrevention(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name   string
		break_ func(*stubRescue)
		want   string
	}{
		{"the mode cannot be read", func(s *stubRescue) { s.readModeErr = boom }, "reading the VLAN mode"},
		{"the mirror cannot be written", func(s *stubRescue) { s.setMirrorErr = boom }, "putting the port mirror back"},
		{"the LED cannot be written", func(s *stubRescue) { s.setLEDErr = boom }, "putting the LED back"},
	} {
		t.Run(c.name, func(t *testing.T) {
			stub := dirtyStub()
			c.break_(stub)
			done, err := dirtySession(stub).Rescue(context.Background())
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q wrapping the cause", err, c.want)
			}
			if len(done) < 2 || !strings.HasPrefix(done[len(done)-2], "IGMP snooping") || !strings.HasPrefix(done[len(done)-1], "loop prevention") {
				t.Errorf("done = %q, want IGMP snooping and loop prevention put back last", done)
			}
			if stub.igmp.Enabled != true || stub.loop != true {
				t.Errorf("IGMP enabled = %t and loop prevention = %t on the switch, want both back to the recorded true", stub.igmp.Enabled, stub.loop)
			}
		})
	}
}

func TestRescueEveryReadFailsAndEachIsNamed(t *testing.T) {
	boom := errors.New("boom")
	stub := &stubRescue{readModeErr: boom, readMirrorErr: boom, readLEDErr: boom, readIGMPErr: boom, readLoopErr: boom}
	done, err := dirtySession(stub).Rescue(context.Background())
	if len(done) != 0 {
		t.Errorf("done = %q, want nothing", done)
	}
	// subtests: one assertion over the one result, not a table of cases
	for _, want := range []string{"VLAN mode", "port mirror", "LED", "IGMP snooping", "loop prevention"} {
		if err == nil || !strings.Contains(err.Error(), "reading the "+want) {
			t.Errorf("err = %v, want the reading of the %s named", err, want)
		}
	}
}

func TestCaptureReportsWhatItCannotReadOfIGMPAndLoopPrevention(t *testing.T) {
	boom := errors.New("boom")
	for _, c := range []struct {
		name string
		stub *stubRescue
		want string
	}{
		{"IGMP", &stubRescue{readIGMPErr: boom}, "IGMP snooping"},
		{"loop prevention", &stubRescue{readLoopErr: boom}, "loop prevention"},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := (&Session{net: c.stub}).capture(context.Background())
			if !errors.Is(err, boom) || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q named", err, c.want)
			}
		})
	}
}

// TestCaptureRecordsIGMPAndLoopPrevention shows that the recording holds the two values SetIGMP
// writes and the loop prevention value, and nothing of the learned groups.
func TestCaptureRecordsIGMPAndLoopPrevention(t *testing.T) {
	stub := &stubRescue{
		igmp: model.IGMP{Enabled: true, ReportSuppression: true, Groups: []model.IGMPGroup{learnedGroup(1)}},
		loop: true,
	}
	s := &Session{net: stub} // zero values: nothing recorded yet
	if err := s.capture(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.igmp != igmpConfig(true, true) {
		t.Errorf("recorded IGMP = %+v, want enabled and suppressed", s.igmp)
	}
	if !s.loop {
		t.Error("recorded loop prevention = false, want true")
	}
}

// TestRescueOrderIsModeMirrorLEDIGMPLoopPrevention fixes the order of the lines when all five
// settings were changed.
func TestRescueOrderIsModeMirrorLEDIGMPLoopPrevention(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	leaveDirty(t, s)
	leaveIGMPAndLoopPreventionChanged(t, s)
	if err := s.Client().SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	done, err := s.Rescue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"VLAN mode", "port mirror", "LED", "IGMP snooping", "loop prevention"}
	if len(done) != len(want) {
		t.Fatalf("rescued %q, want %q", done, want)
	}
	// subtests: one assertion over the one result, not a table of cases
	for i, prefix := range want {
		if !strings.HasPrefix(done[i], prefix) {
			t.Errorf("line %d = %q, want prefix %q", i, done[i], prefix)
		}
	}
}

// leaveIGMPAndLoopPreventionChanged flips IGMP snooping and its report suppression and loop
// prevention on the switch, through the library, as a scenario that failed halfway would leave them.
func leaveIGMPAndLoopPreventionChanged(t *testing.T, s *Session) {
	t.Helper()
	ctx := context.Background()
	igmp, err := s.Client().IGMP(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Client().SetIGMP(ctx, igmpConfig(!igmp.Enabled, !igmp.ReportSuppression)); err != nil {
		t.Fatal(err)
	}
	loop, err := s.Client().LoopPrevention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Client().SetLoopPrevention(ctx, !loop); err != nil {
		t.Fatal(err)
	}
}

// TestGuardRestorePutsBackIGMPAFailedScenarioLeftChanged is the scenario of the hardware runs, on
// the fake: a scenario turns IGMP snooping off and fails before its own cleanup. The net
// registered by GuardRestore puts it back when the test ends, fails the scenario for it, and the
// opening readout matches again.
func TestGuardRestorePutsBackIGMPAFailedScenarioLeftChanged(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	opening, err := s.Client().IGMP(ctx)
	if err != nil || !opening.Enabled || opening.ReportSuppression {
		t.Fatalf("the fake starts with snooping on and suppression off: %+v, err %v", opening, err)
	}
	rec := &cleanupRecorder{TB: t}
	s.GuardRestore(rec)
	if err := s.Client().SetIGMP(ctx, igmpConfig(false, true)); err != nil {
		t.Fatal(err)
	}
	rec.runCleanups()
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "safety net put back the IGMP snooping") {
		t.Errorf("errors = %q, want the scenario failed for IGMP snooping", rec.errors)
	}
	if got, err := s.Client().IGMP(ctx); err != nil || got.Enabled != opening.Enabled || got.ReportSuppression != opening.ReportSuppression {
		t.Errorf("IGMP after the net = %+v, err %v, want the recorded %+v", got, err, opening)
	}
	if diff, err := s.Restored(ctx); err != nil || len(diff) != 0 {
		t.Errorf("after the net: diff %q, err %v, want the opening readout", diff, err)
	}
}

// TestGuardRestorePutsBackLoopPreventionAFailedScenarioLeftChanged is the same for loop prevention.
func TestGuardRestorePutsBackLoopPreventionAFailedScenarioLeftChanged(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	opening, err := s.Client().LoopPrevention(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec := &cleanupRecorder{TB: t}
	s.GuardRestore(rec)
	if err := s.Client().SetLoopPrevention(ctx, !opening); err != nil {
		t.Fatal(err)
	}
	rec.runCleanups()
	if len(rec.errors) != 1 || !strings.Contains(rec.errors[0], "safety net put back the loop prevention") {
		t.Errorf("errors = %q, want the scenario failed for loop prevention", rec.errors)
	}
	if got, err := s.Client().LoopPrevention(ctx); err != nil || got != opening {
		t.Errorf("loop prevention after the net = %t, err %v, want the recorded %t", got, err, opening)
	}
	if diff, err := s.Restored(ctx); err != nil || len(diff) != 0 {
		t.Errorf("after the net: diff %q, err %v, want the opening readout", diff, err)
	}
}
