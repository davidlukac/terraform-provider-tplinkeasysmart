package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/model"
)

// A switch that does not answer costs the whole timeout, with its retries, on every request, and
// the requests of one switch are serialized: a refresh of fifteen resources against a switch that
// is off takes minutes before the plan fails. failFast puts a latch in front of a client: when the
// first request that anything of this client sends gets no answer, every later request fails at once
// with that error, and says so. A switch that has answered once is never latched (a single read it
// does not answer, as some firmware does for some settings, says nothing about the next).
//
// The latch lives as long as the client, which is one provider configuration: the next run of
// Terraform asks again.
type failFastLatch struct {
	mu       sync.Mutex
	answered bool  // some request got an answer
	err      error // the first request that got none, while nothing was answered
}

// refused is the error of a request that is not sent because the switch did not answer before.
func (l *failFastLatch) refused() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		return nil
	}
	return fmt.Errorf("the switch did not answer earlier in this run, so this request was not sent: %w", l.err)
}

// record learns from the outcome of a request: an answer, a refusal or another device's reply are
// answers; silence and a switch that cannot be found are what the latch is for (while nothing was
// answered); anything else (a canceled call, an invalid argument) says nothing about the switch.
func (l *failFastLatch) record(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case err == nil || errors.Is(err, escp.ErrRejected) || errors.Is(err, escp.ErrWrongSwitch):
		l.answered = true
	case errors.Is(err, escp.ErrNoReply) || errors.Is(err, escp.ErrNotFound):
		if !l.answered && l.err == nil {
			l.err = err
		}
	}
}

// failFast returns c with the latch in front of every request; Close and SwitchMAC pass through.
func failFast(c SwitchClient) SwitchClient {
	return latchedClient{SwitchClient: c, latch: &failFastLatch{}}
}

type latchedClient struct {
	SwitchClient
	latch *failFastLatch
}

// do1 and do0 run one request through the latch.
func do0(l *failFastLatch, f func() error) error {
	if err := l.refused(); err != nil {
		return err
	}
	err := f()
	l.record(err)
	return err
}

func do1[T any](l *failFastLatch, f func() (T, error)) (T, error) {
	if err := l.refused(); err != nil {
		var zero T
		return zero, err
	}
	v, err := f()
	l.record(err)
	return v, err
}

func (c latchedClient) Info(ctx context.Context) (model.Info, error) {
	return do1(c.latch, func() (model.Info, error) { return c.SwitchClient.Info(ctx) })
}

func (c latchedClient) LED(ctx context.Context) (bool, error) {
	return do1(c.latch, func() (bool, error) { return c.SwitchClient.LED(ctx) })
}

func (c latchedClient) SetLED(ctx context.Context, on bool) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetLED(ctx, on) })
}

func (c latchedClient) Ports(ctx context.Context) ([]model.Port, error) {
	return do1(c.latch, func() ([]model.Port, error) { return c.SwitchClient.Ports(ctx) })
}

func (c latchedClient) SetPort(ctx context.Context, cfg model.PortConfig) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetPort(ctx, cfg) })
}

func (c latchedClient) PortStats(ctx context.Context) ([]model.PortStats, error) {
	return do1(c.latch, func() ([]model.PortStats, error) { return c.SwitchClient.PortStats(ctx) })
}

func (c latchedClient) LAGs(ctx context.Context) ([]model.LAG, error) {
	return do1(c.latch, func() ([]model.LAG, error) { return c.SwitchClient.LAGs(ctx) })
}

func (c latchedClient) SetLAG(ctx context.Context, lag model.LAG) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetLAG(ctx, lag) })
}

func (c latchedClient) IGMP(ctx context.Context) (model.IGMP, error) {
	return do1(c.latch, func() (model.IGMP, error) { return c.SwitchClient.IGMP(ctx) })
}

func (c latchedClient) SetIGMP(ctx context.Context, cfg model.IGMPConfig) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetIGMP(ctx, cfg) })
}

func (c latchedClient) LoopPrevention(ctx context.Context) (bool, error) {
	return do1(c.latch, func() (bool, error) { return c.SwitchClient.LoopPrevention(ctx) })
}

func (c latchedClient) SetLoopPrevention(ctx context.Context, on bool) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetLoopPrevention(ctx, on) })
}

func (c latchedClient) PortMirror(ctx context.Context) (model.Mirror, error) {
	return do1(c.latch, func() (model.Mirror, error) { return c.SwitchClient.PortMirror(ctx) })
}

func (c latchedClient) SetPortMirror(ctx context.Context, m model.Mirror) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetPortMirror(ctx, m) })
}

func (c latchedClient) MTUVLAN(ctx context.Context) (model.MTUVLAN, error) {
	return do1(c.latch, func() (model.MTUVLAN, error) { return c.SwitchClient.MTUVLAN(ctx) })
}

func (c latchedClient) PortBasedVLAN(ctx context.Context) (model.PortBasedVLAN, error) {
	return do1(c.latch, func() (model.PortBasedVLAN, error) { return c.SwitchClient.PortBasedVLAN(ctx) })
}

func (c latchedClient) SetPortBasedVLAN(ctx context.Context, group model.PortVLAN) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetPortBasedVLAN(ctx, group) })
}

func (c latchedClient) Dot1Q(ctx context.Context) (model.Dot1Q, error) {
	return do1(c.latch, func() (model.Dot1Q, error) { return c.SwitchClient.Dot1Q(ctx) })
}

func (c latchedClient) VLANMode(ctx context.Context) (model.VLANModeSetting, error) {
	return do1(c.latch, func() (model.VLANModeSetting, error) { return c.SwitchClient.VLANMode(ctx) })
}

func (c latchedClient) SetVLANMode(ctx context.Context, s model.VLANModeSetting) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetVLANMode(ctx, s) })
}

func (c latchedClient) SetDot1QVLAN(ctx context.Context, v model.Dot1QVLAN) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetDot1QVLAN(ctx, v) })
}

func (c latchedClient) DeleteDot1QVLAN(ctx context.Context, id model.VLANID) error {
	return do0(c.latch, func() error { return c.SwitchClient.DeleteDot1QVLAN(ctx, id) })
}

func (c latchedClient) SetPVIDs(ctx context.Context, pvids []model.PortPVID) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetPVIDs(ctx, pvids) })
}

func (c latchedClient) QoS(ctx context.Context) (model.QoS, error) {
	return do1(c.latch, func() (model.QoS, error) { return c.SwitchClient.QoS(ctx) })
}

func (c latchedClient) SetQoSMode(ctx context.Context, mode model.QoSMode) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetQoSMode(ctx, mode) })
}

func (c latchedClient) SetPortPriorities(ctx context.Context, changes []model.PortPriority) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetPortPriorities(ctx, changes) })
}

func (c latchedClient) Bandwidth(ctx context.Context) ([]model.PortBandwidth, error) {
	return do1(c.latch, func() ([]model.PortBandwidth, error) { return c.SwitchClient.Bandwidth(ctx) })
}

func (c latchedClient) SetBandwidth(ctx context.Context, limit model.PortBandwidth) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetBandwidth(ctx, limit) })
}

func (c latchedClient) StormControl(ctx context.Context) ([]model.PortStorm, error) {
	return do1(c.latch, func() ([]model.PortStorm, error) { return c.SwitchClient.StormControl(ctx) })
}

func (c latchedClient) SetStormControl(ctx context.Context, cfg model.StormConfig) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetStormControl(ctx, cfg) })
}

func (c latchedClient) System(ctx context.Context) (model.System, error) {
	return do1(c.latch, func() (model.System, error) { return c.SwitchClient.System(ctx) })
}

func (c latchedClient) SetHostname(ctx context.Context, name model.Hostname) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetHostname(ctx, name) })
}

func (c latchedClient) SetIPConfig(ctx context.Context, cfg model.IPConfig) error {
	return do0(c.latch, func() error { return c.SwitchClient.SetIPConfig(ctx, cfg) })
}

func (c latchedClient) EnableDHCP(ctx context.Context) error {
	return do0(c.latch, func() error { return c.SwitchClient.EnableDHCP(ctx) })
}

func (c latchedClient) ConfigBackup(ctx context.Context) (model.ConfigFile, error) {
	return do1(c.latch, func() (model.ConfigFile, error) { return c.SwitchClient.ConfigBackup(ctx) })
}
