package escp

import (
	"context"
	"encoding/binary"
	"fmt"
)

// TLVPortStats is Monitoring > Port Statistics: one 19-byte TLV per port.
const TLVPortStats = 16384

// PortStats is one row of Monitoring > Port Statistics. Counters are the
// switch's 32-bit packet counters and wrap.
type PortStats struct {
	ID      int
	Enabled bool
	Link    string // link-down or negotiated speed
	TxGood  uint32
	TxBad   uint32
	RxGood  uint32
	RxBad   uint32
}

// parsePortStats decodes TLV 16384: port, state, link, tx good, tx bad, rx good, rx bad (4 bytes each).
func parsePortStats(tlvs []TLV) ([]PortStats, error) {
	var out []PortStats
	for _, t := range tlvs {
		if t.Type != TLVPortStats {
			continue
		}
		v := t.Value
		if len(v) < 19 {
			return nil, fmt.Errorf("escp: port statistics TLV too short (%d bytes)", len(v))
		}
		out = append(out, PortStats{
			ID: int(v[0]), Enabled: v[1] == 1, Link: speedName(v[2]),
			TxGood: binary.BigEndian.Uint32(v[3:]), TxBad: binary.BigEndian.Uint32(v[7:]),
			RxGood: binary.BigEndian.Uint32(v[11:]), RxBad: binary.BigEndian.Uint32(v[15:]),
		})
	}
	return out, nil
}

// PortStatistics reads the per-port traffic counters.
func (c *Client) PortStatistics(ctx context.Context) ([]PortStats, error) {
	tlvs, err := c.getTLVs(ctx, TLVPortStats)
	if err != nil {
		return nil, err
	}
	return parsePortStats(tlvs)
}
