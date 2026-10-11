package provider

import (
	"context"
	"errors"
	"sync"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// errInjected is the error every injected fault returns.
var errInjected = errors.New("injected fault: switch unreachable")

// faultyClient wraps a SwitchClient (normally the in-memory fakeClient) and
// injects the failures a real switch produces: a method that errors, an error
// on only the nth call, and a device that accepts writes but ignores them.
type faultyClient struct {
	SwitchClient

	mu           sync.Mutex
	calls        map[string]int
	faults       map[string]faultWindow
	ignoreWrites bool
	ignored      map[string]bool // individual write methods that are accepted but not applied
}

// faultWindow fails calls from..until (1-based, inclusive; until 0 = forever).
type faultWindow struct{ from, until int }

func newFaulty(inner SwitchClient) *faultyClient {
	return &faultyClient{SwitchClient: inner, calls: map[string]int{}, faults: map[string]faultWindow{}, ignored: map[string]bool{}}
}

// failAlways makes every call of method fail.
func (w *faultyClient) failAlways(method string) { w.failCalls(method, 1, 0) }

// failCalls makes calls from..until of method fail.
func (w *faultyClient) failCalls(method string, from, until int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.faults[method] = faultWindow{from, until}
}

// heal removes all injected failures and the ignore-writes mode and resets the call counters.
func (w *faultyClient) heal() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.faults = map[string]faultWindow{}
	w.calls = map[string]int{}
	w.ignoreWrites = false
	w.ignored = map[string]bool{}
}

// ignoreOnly makes one write method accept calls without applying them.
func (w *faultyClient) ignoreOnly(method string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ignored[method] = true
}

// totalCalls is the number of client calls seen, of any method.
func (w *faultyClient) totalCalls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, c := range w.calls {
		n += c
	}
	return n
}

func (w *faultyClient) setIgnoreWrites(v bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ignoreWrites = v
}

// check counts the call and returns the injected error if one is due.
func (w *faultyClient) check(method string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls[method]++
	if f, ok := w.faults[method]; ok && w.calls[method] >= f.from && (f.until == 0 || w.calls[method] <= f.until) {
		return errInjected
	}
	return nil
}

// write is the common path of all Set* methods: injected failure first, then the
// ignore-writes mode (accepted, not applied), then the real call.
func (w *faultyClient) write(method string, do func() error) error {
	if err := w.check(method); err != nil {
		return err
	}
	w.mu.Lock()
	ignore := w.ignoreWrites || w.ignored[method]
	w.mu.Unlock()
	if ignore {
		return nil
	}
	return do()
}

func (w *faultyClient) Info(ctx context.Context) (escp.Info, error) {
	if err := w.check("Info"); err != nil {
		return escp.Info{}, err
	}
	return w.SwitchClient.Info(ctx)
}

func (w *faultyClient) System(ctx context.Context) (escp.System, error) {
	if err := w.check("System"); err != nil {
		return escp.System{}, err
	}
	return w.SwitchClient.System(ctx)
}

func (w *faultyClient) GetLED(ctx context.Context) (bool, error) {
	if err := w.check("GetLED"); err != nil {
		return false, err
	}
	return w.SwitchClient.GetLED(ctx)
}

func (w *faultyClient) SetLED(ctx context.Context, on bool) error {
	return w.write("SetLED", func() error { return w.SwitchClient.SetLED(ctx, on) })
}

func (w *faultyClient) Ports(ctx context.Context) ([]escp.Port, error) {
	if err := w.check("Ports"); err != nil {
		return nil, err
	}
	return w.SwitchClient.Ports(ctx)
}

func (w *faultyClient) SetPort(ctx context.Context, id int, enabled bool, speed string, flow bool) error {
	return w.write("SetPort", func() error { return w.SwitchClient.SetPort(ctx, id, enabled, speed, flow) })
}

func (w *faultyClient) PortStatistics(ctx context.Context) ([]escp.PortStats, error) {
	if err := w.check("PortStatistics"); err != nil {
		return nil, err
	}
	return w.SwitchClient.PortStatistics(ctx)
}

func (w *faultyClient) LAGs(ctx context.Context) ([]escp.LAG, error) {
	if err := w.check("LAGs"); err != nil {
		return nil, err
	}
	return w.SwitchClient.LAGs(ctx)
}

func (w *faultyClient) SetLAG(ctx context.Context, group int, ports []int) error {
	return w.write("SetLAG", func() error { return w.SwitchClient.SetLAG(ctx, group, ports) })
}

func (w *faultyClient) IGMP(ctx context.Context) (escp.IGMP, error) {
	if err := w.check("IGMP"); err != nil {
		return escp.IGMP{}, err
	}
	return w.SwitchClient.IGMP(ctx)
}

func (w *faultyClient) SetIGMP(ctx context.Context, enabled, suppression bool) error {
	return w.write("SetIGMP", func() error { return w.SwitchClient.SetIGMP(ctx, enabled, suppression) })
}

func (w *faultyClient) LoopPrevention(ctx context.Context) (bool, error) {
	if err := w.check("LoopPrevention"); err != nil {
		return false, err
	}
	return w.SwitchClient.LoopPrevention(ctx)
}

func (w *faultyClient) SetLoopPrevention(ctx context.Context, on bool) error {
	return w.write("SetLoopPrevention", func() error { return w.SwitchClient.SetLoopPrevention(ctx, on) })
}

func (w *faultyClient) PortMirror(ctx context.Context) (escp.Mirror, error) {
	if err := w.check("PortMirror"); err != nil {
		return escp.Mirror{}, err
	}
	return w.SwitchClient.PortMirror(ctx)
}

func (w *faultyClient) SetPortMirror(ctx context.Context, m escp.Mirror) error {
	return w.write("SetPortMirror", func() error { return w.SwitchClient.SetPortMirror(ctx, m) })
}

func (w *faultyClient) MTUVLAN(ctx context.Context) (escp.MTUVLAN, error) {
	if err := w.check("MTUVLAN"); err != nil {
		return escp.MTUVLAN{}, err
	}
	return w.SwitchClient.MTUVLAN(ctx)
}

func (w *faultyClient) PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error) {
	if err := w.check("PortBasedVLAN"); err != nil {
		return escp.PortBasedVLAN{}, err
	}
	return w.SwitchClient.PortBasedVLAN(ctx)
}

func (w *faultyClient) SetPortBasedVLAN(ctx context.Context, id int, ports []int) error {
	return w.write("SetPortBasedVLAN", func() error { return w.SwitchClient.SetPortBasedVLAN(ctx, id, ports) })
}

func (w *faultyClient) VLAN8021Q(ctx context.Context) (escp.Dot1Q, error) {
	if err := w.check("VLAN8021Q"); err != nil {
		return escp.Dot1Q{}, err
	}
	return w.SwitchClient.VLAN8021Q(ctx)
}

func (w *faultyClient) QoS(ctx context.Context) (escp.QoS, error) {
	if err := w.check("QoS"); err != nil {
		return escp.QoS{}, err
	}
	return w.SwitchClient.QoS(ctx)
}

func (w *faultyClient) SetQoSMode(ctx context.Context, mode int) error {
	return w.write("SetQoSMode", func() error { return w.SwitchClient.SetQoSMode(ctx, mode) })
}

func (w *faultyClient) SetPortPriorities(ctx context.Context, changes map[int]int) error {
	return w.write("SetPortPriorities", func() error { return w.SwitchClient.SetPortPriorities(ctx, changes) })
}

func (w *faultyClient) Bandwidth(ctx context.Context) ([]escp.PortBandwidth, error) {
	if err := w.check("Bandwidth"); err != nil {
		return nil, err
	}
	return w.SwitchClient.Bandwidth(ctx)
}

func (w *faultyClient) SetBandwidth(ctx context.Context, port, in, eg int) error {
	return w.write("SetBandwidth", func() error { return w.SwitchClient.SetBandwidth(ctx, port, in, eg) })
}

func (w *faultyClient) StormControl(ctx context.Context) ([]escp.PortStorm, error) {
	if err := w.check("StormControl"); err != nil {
		return nil, err
	}
	return w.SwitchClient.StormControl(ctx)
}

func (w *faultyClient) SetStormControl(ctx context.Context, port int, uu, mc, bc bool, rate int) error {
	return w.write("SetStormControl", func() error { return w.SwitchClient.SetStormControl(ctx, port, uu, mc, bc, rate) })
}
