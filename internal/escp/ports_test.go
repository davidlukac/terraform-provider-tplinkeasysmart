package escp

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestSetPortWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	// 1000Mbps-full: flow control with a fixed 10 or 100 Mbps speed is refused (TestSetPortRefusesFlowControlAtAFixedSpeed).
	if err := c.SetPort(context.Background(), PortConfig{ID: 7, Enabled: true, Speed: Speed1000Full, FlowControl: true}); err != nil {
		t.Fatal(err)
	}
	got := f.Port7Setting()
	want := []byte{7, 1, 0, 6, 0, 1, 0}
	if string(got) != string(want) {
		t.Fatalf("wire bytes %v, want %v", got, want)
	}
	if err := c.SetPort(context.Background(), PortConfig{ID: 7, Enabled: true, Speed: "warp", FlowControl: false}); err == nil {
		t.Fatal("unknown speed must be rejected before sending")
	}
	if err := c.SetPort(context.Background(), PortConfig{ID: 0, Enabled: true, Speed: "auto", FlowControl: false}); err == nil {
		t.Fatal("port 0 must be rejected")
	}
}

// Admin state and flow control are separate bytes: one set and the other clear tells a swapped
// field apart, which TestSetPortWire (both set) cannot.
func TestSetPortWireStateAndFlowControlAreNotSwapped(t *testing.T) {
	cases := []struct {
		name string
		cfg  PortConfig
		want []byte
	}{
		{"enabled, no flow control", PortConfig{ID: 7, Enabled: true, Speed: SpeedAuto}, []byte{7, 1, 0, 1, 0, 0, 0}},
		{"disabled, flow control", PortConfig{ID: 7, Speed: SpeedAuto, FlowControl: true}, []byte{7, 0, 0, 1, 0, 1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			if err := c.SetPort(context.Background(), tc.cfg); err != nil {
				t.Fatal(err)
			}
			got := f.Port7Setting()
			if string(got) != string(tc.want) {
				t.Fatalf("wire bytes %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSpeedNameUnknownCode(t *testing.T) {
	if got := speedFromCode(9); got != "unknown(9)" {
		t.Fatalf("got %q", got)
	}
	if got := speedFromCode(0); got != "link-down" {
		t.Fatalf("got %q", got)
	}
}

// Half duplex on a port that is a LAG member was refused with code 7407 on a real switch
// (switch 03, firmware 20250710, ports 3 and 4 in LAG1); the error must say why.
func TestSetPortRefusalNamesTheLAGDuplexRule(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom, f.RejectCode = 1, lagRefusedHalfDuplex })
	c := dialFake(t, f, "secret")
	err := c.SetPort(context.Background(), PortConfig{ID: 3, Enabled: true, Speed: Speed100Half})
	var rejected *RejectedError
	if !errors.Is(err, ErrRejected) || !errors.As(err, &rejected) || rejected.Code != 7407 {
		t.Fatalf("got %v, want a RejectedError with code 7407 that matches ErrRejected", err)
	}
	if !strings.Contains(err.Error(), "member of a LAG") || !strings.Contains(err.Error(), "full duplex") || strings.Contains(err.Error(), "wrong credentials") {
		t.Fatalf("message %q must name the LAG duplex rule and not blame the credentials", err)
	}
}

// Only the code that was provoked is named; any other refusal keeps the generic message, and
// an accepted write is unaffected.
func TestSetPortRefusalOfAnotherCodeStaysGeneric(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom, f.RejectCode = 1, 7406 })
	c := dialFake(t, f, "secret")
	err := c.SetPort(context.Background(), PortConfig{ID: 3, Enabled: true, Speed: SpeedAuto})
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "error code 7406") || !strings.Contains(err.Error(), "wrong credentials") {
		t.Fatalf("got %v", err)
	}
	g := escptest.Start(t)
	if err := dialFake(t, g, "secret").SetPort(context.Background(), PortConfig{ID: 3, Enabled: true, Speed: SpeedAuto}); err != nil {
		t.Fatalf("an accepted write must stay accepted: %v", err)
	}
}

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

func tlvHex(t *testing.T, typ wire.TLVType, h string) wire.TLV {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return wire.TLV{Type: typ, Value: b}
}

// Values captured from a TL-SG108E v6 (ports 1+2 in LAG1, ports 1,2,4,8 linked at 1 Gbps).
func TestParsePorts(t *testing.T) {
	got, err := parsePorts([]wire.TLV{
		tlvHex(t, wire.TLVPorts, "01010101060000"),
		tlvHex(t, wire.TLVPorts, "03010001000000"),
		tlvHex(t, wire.TLVPorts, "08010001060000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Port{
		{ID: 1, Enabled: true, LAG: 1, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
		{ID: 3, Enabled: true, LAG: 0, ConfiguredSpeed: "auto", ActualSpeed: "link-down"},
		{ID: 8, Enabled: true, LAG: 0, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestParsePortsShort(t *testing.T) {
	if _, err := parsePorts([]wire.TLV{{Type: wire.TLVPorts, Value: []byte{1, 1}}}); err == nil {
		t.Fatal("expected error for short TLV")
	}
}

func TestSpeedCodes(t *testing.T) {
	for i, n := range ConfigurableSpeeds() {
		got, err := n.code()
		if err != nil || int(got) != i+1 || speedFromCode(got) != n {
			t.Errorf("%s: code %d err %v name %s", n, got, err, speedFromCode(got))
		}
	}
	for _, bad := range []Speed{"warp", "", SpeedLinkDown} {
		if _, err := bad.code(); err == nil {
			t.Errorf("%q: expected an error, it cannot be configured", bad)
		}
	}
}

// The spellings are part of the provider's Terraform state and of the web UI: they must not drift.
func TestSpeedSpellings(t *testing.T) {
	want := []string{"auto", "10Mbps-half", "10Mbps-full", "100Mbps-half", "100Mbps-full", "1000Mbps-full"}
	got := ConfigurableSpeeds()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, s := range got {
		if s.String() != want[i] {
			t.Errorf("speed %d is %q, want %q", i+1, s, want[i])
		}
	}
	if SpeedLinkDown.String() != "link-down" {
		t.Errorf("SpeedLinkDown is %q", SpeedLinkDown)
	}
}

func TestParseSpeed(t *testing.T) {
	for _, s := range ConfigurableSpeeds() {
		if got, err := ParseSpeed(s.String()); err != nil || got != s {
			t.Errorf("ParseSpeed(%q) = %q, %v", s, got, err)
		}
	}
	for _, bad := range []string{"warp", "", "link-down", "AUTO"} {
		if _, err := ParseSpeed(bad); err == nil {
			t.Errorf("ParseSpeed(%q) succeeded", bad)
		}
	}
}

// The returned slice is a copy: a caller cannot change the set the client accepts.
func TestConfigurableSpeedsIsACopy(t *testing.T) {
	s := ConfigurableSpeeds()
	s[0] = "tampered"
	if got := ConfigurableSpeeds()[0]; got != SpeedAuto {
		t.Fatalf("first speed is now %q", got)
	}
}
