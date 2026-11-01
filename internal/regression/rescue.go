package regression

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/davidlukac/escp-go"
)

// rescueTimeout bounds the safety net: it runs when a scenario is over, whatever state the
// switch is in, and must not hang the run.
const rescueTimeout = 60 * time.Second

// rescueClient is what the safety net needs of a client: the two settings, read and written.
// *escp.Client satisfies it; the tests of the error paths use a stub.
type rescueClient interface {
	VLANMode(ctx context.Context) (escp.VLANModeSetting, error)
	SetVLANMode(ctx context.Context, s escp.VLANModeSetting) error
	PortMirror(ctx context.Context) (escp.Mirror, error)
	SetPortMirror(ctx context.Context, m escp.Mirror) error
}

// Rescue puts back the two settings a scenario that failed halfway can leave behind and that
// Terraform cannot undo, because destroying a VLAN mode or a mirror only forgets it: the VLAN
// mode and the port mirror. Each is compared with the state recorded when the session opened and
// written back only when it differs, so a scenario that cleaned up after itself causes no write.
// It returns one line per setting it changed. Everything else the scenarios touch is not
// restored here: the readout comparison (Restored) names it, and the operator decides.
//
// The mode goes first: switching it discards the settings of the other modes, and the mirror is
// independent of them.
func (s *Session) Rescue(ctx context.Context) ([]string, error) {
	var done []string
	mode, err := s.net.VLANMode(ctx)
	if err != nil {
		return done, fmt.Errorf("reading the VLAN mode: %w", err)
	}
	if mode != s.mode {
		if err := s.net.SetVLANMode(ctx, s.mode); err != nil {
			return done, fmt.Errorf("putting the VLAN mode back to %+v: %w", s.mode, err)
		}
		done = append(done, fmt.Sprintf("VLAN mode %+v, was left at %+v", s.mode, mode))
	}
	mirror, err := s.net.PortMirror(ctx)
	if err != nil {
		return done, fmt.Errorf("reading the port mirror: %w", err)
	}
	if mirror != s.mirror {
		if err := s.net.SetPortMirror(ctx, s.mirror); err != nil {
			return done, fmt.Errorf("putting the port mirror back to %+v: %w", s.mirror, err)
		}
		done = append(done, fmt.Sprintf("port mirror %+v, was left at %+v", s.mirror, mirror))
	}
	return done, nil
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
