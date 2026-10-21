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

// PortStorm is a port's storm control setting.
type PortStorm struct {
	Port           int
	UnknownUnicast bool
	Multicast      bool
	Broadcast      bool
	RateKbps       int
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
		p := PortStorm{Port: int(v[stormOffPort]), UnknownUnicast: v[stormOffUnknownUnicast] != 0, Multicast: v[stormOffMulticast] != 0,
			Broadcast: v[stormOffBroadcast] != 0, RateKbps: int(binary.BigEndian.Uint32(v[stormOffRate:]))}
		p.Enabled = p.RateKbps != 0 && (p.UnknownUnicast || p.Multicast || p.Broadcast)
		out = append(out, p)
	}
	return out, nil
}

// StormControl reads the per-port storm control settings.
func (c *Client) StormControl(ctx context.Context) ([]PortStorm, error) {
	tlvs, err := c.getTLVs(ctx, wire.TLVStormControl)
	if err != nil {
		return nil, err
	}
	return parseStorm(tlvs)
}

// SetStormControl configures storm control on one port. Following the vendor
// utility: selecting any traffic type requires a rate (a multiple of 64 kbps,
// at least 64, at most MaxBandwidthKbps); selecting none disables it and the
// rate must be 0. A rate on a port that carries traffic drops its excess
// broadcast/multicast; callers must guard against that.
func (c *Client) SetStormControl(ctx context.Context, port int, unknownUnicast, multicast, broadcast bool, rateKbps int) error {
	if err := ValidatePort(port); err != nil {
		return err
	}
	any := unknownUnicast || multicast || broadcast
	switch {
	case any && (rateKbps < RateStepKbps || !validateRateKbps(rateKbps)):
		return fmt.Errorf("escp: storm control on port %d needs a rate that is a multiple of %d kbps between %d and %d, got %d", port, RateStepKbps, RateStepKbps, MaxBandwidthKbps, rateKbps)
	case !any && rateKbps != 0:
		return fmt.Errorf("escp: storm control on port %d has a rate (%d) but no traffic type selected", port, rateKbps)
	}
	v := make([]byte, stormEntryLen)
	v[stormOffPort] = byte(port)
	v[stormOffEnabled] = boolByte(any)
	v[stormOffUnknownUnicast], v[stormOffMulticast], v[stormOffBroadcast] = boolByte(unknownUnicast), boolByte(multicast), boolByte(broadcast)
	binary.BigEndian.PutUint32(v[stormOffRate:], uint32(rateKbps))
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVStormControl, Value: v})
}
