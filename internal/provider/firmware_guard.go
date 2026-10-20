package provider

import (
	"context"
	"fmt"
	"sync"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// firmwareGuard wraps a SwitchClient and refuses every write while the
// switch runs a firmware build the provider has not been verified against
// (escp.VerifiedFirmwareBuilds), unless the user opted in with
// allow_unverified_firmware. Reads always pass: they cannot change the switch,
// and they are how a new firmware is fingerprinted. The firmware is read once,
// before the first write; a failed read refuses the write and is retried.
type firmwareGuard struct {
	SwitchClient

	mu       sync.Mutex
	verified bool
}

// newFirmwareGuard returns inner behind the guard; with allowUnverified the
// guard lets everything through.
func newFirmwareGuard(inner SwitchClient, allowUnverified bool) SwitchClient {
	if allowUnverified {
		return inner
	}
	return &firmwareGuard{SwitchClient: inner}
}

// check returns nil once the firmware is known to be verified.
func (g *firmwareGuard) check(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.verified {
		return nil
	}
	info, err := g.Info(ctx)
	if err != nil {
		return fmt.Errorf("refusing to write: cannot read the firmware version to check it: %w", err)
	}
	if err := escp.CheckFirmwareVerified(info.Firmware); err != nil {
		return fmt.Errorf("refusing to write: %w. Reads work on any firmware; set allow_unverified_firmware = true (provider or switch) to write anyway, at your own risk", err)
	}
	g.verified = true
	return nil
}

func (g *firmwareGuard) SetLED(ctx context.Context, on bool) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetLED(ctx, on)
}

func (g *firmwareGuard) SetPort(ctx context.Context, id int, enabled bool, speed string, flowControl bool) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPort(ctx, id, enabled, speed, flowControl)
}

func (g *firmwareGuard) SetLAG(ctx context.Context, group int, ports []int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetLAG(ctx, group, ports)
}

func (g *firmwareGuard) SetIGMP(ctx context.Context, enabled, suppression bool) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetIGMP(ctx, enabled, suppression)
}

func (g *firmwareGuard) SetLoopPrevention(ctx context.Context, on bool) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetLoopPrevention(ctx, on)
}

func (g *firmwareGuard) SetPortMirror(ctx context.Context, m escp.Mirror) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortMirror(ctx, m)
}

func (g *firmwareGuard) SetMTUVLAN(ctx context.Context, enabled bool, uplinkPort int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetMTUVLAN(ctx, enabled, uplinkPort)
}

func (g *firmwareGuard) SetPortBasedVLAN(ctx context.Context, id int, ports []int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortBasedVLAN(ctx, id, ports)
}

func (g *firmwareGuard) SetPortBasedVLANMode(ctx context.Context, enabled bool) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortBasedVLANMode(ctx, enabled)
}

func (g *firmwareGuard) SetVLAN8021QMode(ctx context.Context, enabled bool) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetVLAN8021QMode(ctx, enabled)
}

func (g *firmwareGuard) SetVLAN8021Q(ctx context.Context, v escp.VLAN8021Q) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetVLAN8021Q(ctx, v)
}

func (g *firmwareGuard) DeleteVLAN8021Q(ctx context.Context, id int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.DeleteVLAN8021Q(ctx, id)
}

func (g *firmwareGuard) SetPVIDs(ctx context.Context, pvids map[int]int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPVIDs(ctx, pvids)
}

func (g *firmwareGuard) SetQoSMode(ctx context.Context, mode int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetQoSMode(ctx, mode)
}

func (g *firmwareGuard) SetPortPriorities(ctx context.Context, changes map[int]int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortPriorities(ctx, changes)
}

func (g *firmwareGuard) SetBandwidth(ctx context.Context, port, ingressKbps, egressKbps int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetBandwidth(ctx, port, ingressKbps, egressKbps)
}

func (g *firmwareGuard) SetStormControl(ctx context.Context, port int, unknownUnicast, multicast, broadcast bool, rateKbps int) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetStormControl(ctx, port, unknownUnicast, multicast, broadcast, rateKbps)
}

func (g *firmwareGuard) SetHostname(ctx context.Context, name string) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetHostname(ctx, name)
}

func (g *firmwareGuard) SetIPConfig(ctx context.Context, cfg escp.IPConfig) error {
	if err := g.check(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetIPConfig(ctx, cfg)
}
