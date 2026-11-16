package regression

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/davidlukac/escp-go/model"
)

// rescueTimeout bounds the safety net: it runs when a scenario is over, whatever state the
// switch is in, and must not hang the run.
const rescueTimeout = 60 * time.Second

// rescueClient is what the safety net needs of a client: the five settings, read and written.
// *escp.Client satisfies it; the tests of the error paths use a stub.
type rescueClient interface {
	VLANMode(ctx context.Context) (model.VLANModeSetting, error)
	SetVLANMode(ctx context.Context, s model.VLANModeSetting) error
	PortMirror(ctx context.Context) (model.Mirror, error)
	SetPortMirror(ctx context.Context, m model.Mirror) error
	LED(ctx context.Context) (bool, error)
	SetLED(ctx context.Context, on bool) error
	IGMP(ctx context.Context) (model.IGMP, error)
	SetIGMP(ctx context.Context, cfg model.IGMPConfig) error
	LoopPrevention(ctx context.Context) (bool, error)
	SetLoopPrevention(ctx context.Context, on bool) error
}

// Rescue puts back the five settings a scenario that failed halfway can leave behind and that
// Terraform cannot undo, because destroying a VLAN mode, a mirror, the LED, IGMP snooping or loop
// prevention only forgets it: the VLAN mode, the port mirror, the LED (the harmless probe most
// scenarios apply, so the one a failed scenario most often leaves off), IGMP snooping (snooping
// and report suppression) and loop prevention. Each is compared with the state recorded when the
// session opened and written back only when it differs, so a scenario that cleaned up after itself
// causes no write. It returns one line per setting it changed. The multicast groups the switch has
// learned are runtime data: they are neither compared nor written. Everything else the scenarios
// touch is not restored here: the readout comparison (Restored) names it, and the operator decides.
//
// The settings are independent: a restore that fails (a read, or the write) never keeps the next
// one from running, because the net is the last chance to leave the switch as it was found. The
// errors of every setting that failed are joined (each names its setting), and the lines of every
// change that was made are returned with them. The one failure that stops the net is having no
// client at all.
//
// The mode goes first: switching it discards the settings of the other modes, and the other four
// settings are independent of them. The order is the mode, the mirror, the LED, IGMP snooping,
// loop prevention.
//
// The net restores to the state recorded when the session opened: a setting that an earlier,
// crashed run left changed is the baseline of this one, so the operator must start from the
// configured state.
//
// readwrite: each setting is read and compared with the one recorded when the session opened, then
// written back as that recorded value (never one derived from the read), and the library allows one client
// per switch, so the session's client is the only writer in this process in between.
func (s *Session) Rescue(ctx context.Context) ([]string, error) {
	net, err := s.rescuer()
	if err != nil {
		return nil, err
	}
	var done []string
	var errs []error
	for _, step := range []func() (string, error){
		func() (string, error) {
			return restoreSetting(ctx, "VLAN mode", s.mode, net.VLANMode, net.SetVLANMode)
		},
		func() (string, error) {
			return restoreSetting(ctx, "port mirror", s.mirror, net.PortMirror, net.SetPortMirror)
		},
		func() (string, error) { return restoreSetting(ctx, "LED", s.led, net.LED, net.SetLED) },
		func() (string, error) {
			return restoreSetting(ctx, "IGMP snooping", s.igmp, igmpConfigOf(net), net.SetIGMP)
		},
		func() (string, error) {
			return restoreSetting(ctx, "loop prevention", s.loop, net.LoopPrevention, net.SetLoopPrevention)
		},
	} {
		line, err := step()
		if err != nil {
			errs = append(errs, err)
		}
		if line != "" {
			done = append(done, line)
		}
	}
	return done, errors.Join(errs...)
}

// igmpConfigOf reads IGMP snooping as what SetIGMP writes: the two values the net compares and
// restores. The learned groups of the reading are runtime data and are left out, so a switch whose
// groups changed during the scenario is not reported as changed.
func igmpConfigOf(net rescueClient) func(context.Context) (model.IGMPConfig, error) {
	return func(ctx context.Context) (model.IGMPConfig, error) {
		v, err := net.IGMP(ctx)
		return model.IGMPConfig{Enabled: v.Enabled, ReportSuppression: v.ReportSuppression}, err
	}
}

// restoreSetting reads one setting and, when it differs from the recorded value, writes the
// recorded value back. It returns the report line of a change that was made, or the error naming
// the setting.
func restoreSetting[T comparable](ctx context.Context, name string, recorded T,
	read func(context.Context) (T, error), write func(context.Context, T) error) (string, error) {
	current, err := read(ctx)
	if err != nil {
		return "", fmt.Errorf("reading the %s: %w", name, err)
	}
	if current == recorded {
		return "", nil
	}
	if err := write(ctx, recorded); err != nil {
		return "", fmt.Errorf("putting the %s back to %+v: %w", name, recorded, err)
	}
	return fmt.Sprintf("%s %+v, was left at %+v", name, recorded, current), nil
}

// GuardRestore runs Rescue when the test ends, whether it passed or failed, and fails the test
// for every setting it had to put back: a scenario that needs the safety net is a scenario that
// did not restore the switch itself. It must be registered after the session's client is open
// (cleanups run last-in first, so the net runs before the client closes).
func (s *Session) GuardRestore(tb testing.TB) {
	tb.Helper()
	tb.Cleanup(func() { s.rescueAtEnd(tb) })
}

func (s *Session) rescueAtEnd(tb testing.TB) {
	tb.Helper()
	s.own() // the provider may still hold the switch when a scenario stopped in the middle of a step
	ctx, cancel := context.WithTimeout(context.Background(), rescueTimeout)
	defer cancel()
	done, err := s.Rescue(ctx)
	for _, d := range done {
		tb.Errorf("the scenario left the switch changed; the safety net put back the %s", d)
	}
	if err != nil {
		tb.Errorf("the safety net could not finish: %v; compare the switch with the opening readout by hand", err)
	}
}
