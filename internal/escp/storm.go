package escp

import (
	"context"
	"encoding/binary"
	"fmt"
)

// TLVStormControl is QoS > Storm Control: one TLV per port, 9 bytes:
// port(1) reserved(1) unknown-unicast(1) multicast(1) broadcast(1) rate(4, kbps).
const TLVStormControl = 12800

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

func parseStorm(tlvs []TLV) ([]PortStorm, error) {
	var out []PortStorm
	for _, t := range tlvs {
		if t.Type != TLVStormControl {
			continue
		}
		v := t.Value
		if len(v) != 9 {
			return nil, fmt.Errorf("escp: storm control entry has %d bytes, want 9", len(v))
		}
		p := PortStorm{Port: int(v[0]), UnknownUnicast: v[2] != 0, Multicast: v[3] != 0, Broadcast: v[4] != 0,
			RateKbps: int(binary.BigEndian.Uint32(v[5:]))}
		p.Enabled = p.RateKbps != 0 && (p.UnknownUnicast || p.Multicast || p.Broadcast)
		out = append(out, p)
	}
	return out, nil
}

// StormControl reads the per-port storm control settings.
func (c *Client) StormControl(ctx context.Context) ([]PortStorm, error) {
	tlvs, err := c.getTLVs(ctx, TLVStormControl)
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
	if port < 1 || port > 32 {
		return fmt.Errorf("escp: invalid port %d", port)
	}
	any := unknownUnicast || multicast || broadcast
	switch {
	case any && (rateKbps < 64 || rateKbps > MaxBandwidthKbps || rateKbps%64 != 0):
		return fmt.Errorf("escp: storm control on port %d needs a rate that is a multiple of 64 kbps between 64 and %d, got %d", port, MaxBandwidthKbps, rateKbps)
	case !any && rateKbps != 0:
		return fmt.Errorf("escp: storm control on port %d has a rate (%d) but no traffic type selected", port, rateKbps)
	}
	v := make([]byte, 9)
	v[0] = byte(port)
	v[1] = boolByte(any)
	v[2], v[3], v[4] = boolByte(unknownUnicast), boolByte(multicast), boolByte(broadcast)
	binary.BigEndian.PutUint32(v[5:], uint32(rateKbps))
	return c.setTLVs(ctx, TLV{TLVStormControl, v})
}
