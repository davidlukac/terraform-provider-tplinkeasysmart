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
