package escp

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Layout of a storm control entry.
const (
	stormOffPort           = 0
	stormOffEnabled        = 1
	stormOffUnknownUnicast = 2
	stormOffMulticast      = 3
	stormOffBroadcast      = 4
	stormOffRate           = 5
	stormEntryLen          = stormOffRate + 4
)

// PortStorm is a port's storm control setting: which kinds of traffic are limited and at what rate.
type PortStorm struct {
	Port           PortID
	UnknownUnicast bool // limit unknown-unicast traffic
	Multicast      bool // limit multicast traffic
	Broadcast      bool // limit broadcast traffic
	RateKbps       Kbps // the limit, a multiple of RateStepKbps; 0 when none is set
	// Enabled mirrors the web UI: a rate is set and at least one traffic type is selected.
	Enabled bool
}

func parseStorm(tlvs []wire.TLV) ([]PortStorm, error) {
	var out []PortStorm
	for _, t := range tlvs {
		if t.Type != wire.TLVStormControl {
			continue
		}
		v := t.Value
		if len(v) != stormEntryLen {
			return nil, fmt.Errorf("escp: storm control entry has %d bytes, want %d", len(v), stormEntryLen)
		}
		p := PortStorm{Port: PortID(v[stormOffPort]), UnknownUnicast: v[stormOffUnknownUnicast] != 0, Multicast: v[stormOffMulticast] != 0,
			Broadcast: v[stormOffBroadcast] != 0, RateKbps: Kbps(binary.BigEndian.Uint32(v[stormOffRate:]))}
		p.Enabled = p.RateKbps != 0 && (p.UnknownUnicast || p.Multicast || p.Broadcast)
		out = append(out, p)
	}
	return out, nil
}

// StormControl reads the storm control setting of every port.
func (c *Client) StormControl(ctx context.Context) ([]PortStorm, error) {
	tlvs, err := c.getTLVs(ctx, wire.TLVStormControl)
	if err != nil {
		return nil, err
	}
	return parseStorm(tlvs)
}

// StormConfig is what SetStormControl writes to one port. Unlike PortStorm (a reading)
// it has no Enabled flag: storm control is on exactly when a traffic type is selected.
type StormConfig struct {
	Port           PortID
	UnknownUnicast bool
	Multicast      bool
	Broadcast      bool
	RateKbps       Kbps
}

// SetStormControl configures storm control on one port. Following the vendor
// utility: selecting any traffic type requires a rate (a multiple of 64 kbps,
// at least 64, at most MaxBandwidthKbps); selecting none disables it and the
// rate must be 0. A rate on a port that carries traffic drops its excess
// broadcast/multicast; callers must guard against that.
func (c *Client) SetStormControl(ctx context.Context, cfg StormConfig) error {
	if err := cfg.Port.Validate(); err != nil {
		return err
	}
	any := cfg.UnknownUnicast || cfg.Multicast || cfg.Broadcast
	switch {
	case any && (cfg.RateKbps < RateStepKbps || cfg.RateKbps.Validate() != nil):
		return invalidf("escp: storm control on port %d needs a rate that is a multiple of %d kbps between %d and %d, got %d", cfg.Port, RateStepKbps, RateStepKbps, MaxBandwidthKbps, cfg.RateKbps)
	case !any && cfg.RateKbps != 0:
		return invalidf("escp: storm control on port %d has a rate (%d) but no traffic type selected", cfg.Port, cfg.RateKbps)
	}
	if err := c.requirePorts(ctx, cfg.Port.set()); err != nil {
		return err
	}
	v := make([]byte, stormEntryLen)
	v[stormOffPort] = byte(cfg.Port)
	v[stormOffEnabled] = boolByte(any)
	v[stormOffUnknownUnicast], v[stormOffMulticast], v[stormOffBroadcast] = boolByte(cfg.UnknownUnicast), boolByte(cfg.Multicast), boolByte(cfg.Broadcast)
	binary.BigEndian.PutUint32(v[stormOffRate:], uint32(cfg.RateKbps))
	return c.setTLVs(ctx, Operation{Name: "SetStormControl", Ports: cfg.Port.set(), Impact: ImpactTraffic}, wire.TLV{Type: wire.TLVStormControl, Value: v})
}
