package escp

import (
	"context"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

// The rule: flow control stays on only with auto or 1000Mbps-full. Measured on a switch
// (firmware 20250710, port 2) and enforced by the vendor utility's own port dialog.
func TestSpeedAllowsFlowControl(t *testing.T) {
	want := map[Speed]bool{
		SpeedAuto: true, Speed10Half: false, Speed10Full: false,
		Speed100Half: false, Speed100Full: false, Speed1000Full: true,
	}
	if len(want) != len(ConfigurableSpeeds()) {
		t.Fatalf("the table covers %d speeds, the client knows %d: a new speed needs a decision here", len(want), len(ConfigurableSpeeds()))
	}
	for _, s := range ConfigurableSpeeds() {
		if got := s.AllowsFlowControl(); got != want[s] {
			t.Errorf("%s.AllowsFlowControl() = %v, want %v", s, got, want[s])
		}
	}
	if SpeedLinkDown.AllowsFlowControl() || Speed("warp").AllowsFlowControl() {
		t.Error("a speed that cannot be configured allows nothing")
	}
}

// The switch would take the write and silently keep flow control off, leaving the speed changed.
// The client refuses it before sending anything, not even the key exchange's follow-up SET.
func TestSetPortRefusesFlowControlAtAFixedSpeed(t *testing.T) {
	for _, speed := range []Speed{Speed10Half, Speed10Full, Speed100Half, Speed100Full} {
		t.Run(string(speed), func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			err := c.SetPort(context.Background(), PortConfig{ID: 5, Enabled: true, Speed: speed, FlowControl: true})
			if err == nil {
				t.Fatalf("flow control at %s must be refused", speed)
			}
			for _, part := range []string{"port 5", "flow control needs speed auto or 1000Mbps-full", string(speed)} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("the refusal %q does not mention %q", err, part)
				}
			}
			if sets := f.AllSets(); len(sets) != 0 {
				t.Fatalf("the switch received %d write(s) despite the refusal: %v", len(sets), sets)
			}
			// the same speed without flow control is an ordinary write
			if err := c.SetPort(context.Background(), PortConfig{ID: 5, Enabled: true, Speed: speed}); err != nil {
				t.Fatalf("%s without flow control: %v", speed, err)
			}
			if sets := f.AllSets(); len(sets) != 1 {
				t.Fatalf("want exactly the one allowed write, got %d: %v", len(sets), sets)
			}
		})
	}
}

// The refusal is about the combination only: every speed on the wire with flow control off,
// and flow control on where it is allowed, carry the speed code in its own byte and the
// flow control flag in its own. Port 7 is the one the fake records.
func TestSetPortWireEverySpeedAndTheFlowControlThatIsAllowed(t *testing.T) {
	codes := map[Speed]byte{SpeedAuto: 1, Speed10Half: 2, Speed10Full: 3, Speed100Half: 4, Speed100Full: 5, Speed1000Full: 6}
	for _, speed := range ConfigurableSpeeds() {
		for _, flow := range []bool{false, true} {
			if flow && !speed.AllowsFlowControl() {
				continue
			}
			t.Run(string(speed)+boolSuffix(flow), func(t *testing.T) {
				f := escptest.Start(t)
				c := dialFake(t, f, "secret")
				if err := c.SetPort(context.Background(), PortConfig{ID: 7, Enabled: true, Speed: speed, FlowControl: flow}); err != nil {
					t.Fatal(err)
				}
				want := []byte{7, 1, 0, codes[speed], 0, 0, 0}
				if flow {
					want[5] = 1
				}
				if got := f.Port7Setting(); string(got) != string(want) {
					t.Fatalf("wire bytes %v, want %v", got, want)
				}
			})
		}
	}
}

func boolSuffix(flow bool) string {
	if flow {
		return "+flow"
	}
	return ""
}
