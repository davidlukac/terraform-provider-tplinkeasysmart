package escp

import (
	"context"
	"encoding/binary"
	"fmt"
)

// TLVPortStats is Monitoring > Port Statistics: one 19-byte TLV per port.
const TLVPortStats = 16384

// Layout of a port statistics entry: port, state, link speed (1 byte each),
// then four 4-byte counters.
const (
	statsOffPort   = 0
	statsOffState  = 1
	statsOffLink   = 2
	statsOffTxGood = 3
	statsOffTxBad  = statsOffTxGood + 4
	statsOffRxGood = statsOffTxBad + 4
	statsOffRxBad  = statsOffRxGood + 4
	statsEntryLen  = statsOffRxBad + 4
)

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
		if len(v) < statsEntryLen {
			return nil, fmt.Errorf("escp: port statistics TLV too short (%d bytes)", len(v))
		}
		out = append(out, PortStats{
			ID: int(v[statsOffPort]), Enabled: v[statsOffState] == 1, Link: speedName(v[statsOffLink]),
			TxGood: binary.BigEndian.Uint32(v[statsOffTxGood:]), TxBad: binary.BigEndian.Uint32(v[statsOffTxBad:]),
			RxGood: binary.BigEndian.Uint32(v[statsOffRxGood:]), RxBad: binary.BigEndian.Uint32(v[statsOffRxBad:]),
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
