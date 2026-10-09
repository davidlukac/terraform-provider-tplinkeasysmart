package escp

import (
	"context"
	"fmt"
)

// TLV types of QoS > QoS Basic.
const (
	TLVQoSMode     = 12288 // 1 byte
	TLVQoSPriority = 12289 // one TLV per port: port(1) priority(1), raw 0..3
)

// QoSModeNames maps the switch's mode byte to the web UI names.
var QoSModeNames = map[int]string{0: "port-based", 1: "802.1p", 2: "dscp-802.1p"}

// PortPriority is a port's priority queue as shown in the web UI: 1 (lowest) to 4 (highest).
type PortPriority struct {
	Port     int
	Priority int
}

// QoS is QoS > QoS Basic.
type QoS struct {
	Mode       int
	ModeName   string
	Priorities []PortPriority
}

func parseQoS(tlvs []TLV) (QoS, error) {
	out := QoS{Priorities: []PortPriority{}}
	seen := false
	for _, t := range tlvs {
		switch t.Type {
		case TLVQoSMode:
			if len(t.Value) != 1 {
				return out, fmt.Errorf("escp: QoS mode TLV has %d bytes", len(t.Value))
			}
			out.Mode, seen = int(t.Value[0]), true
			out.ModeName = QoSModeNames[out.Mode]
			if out.ModeName == "" {
				out.ModeName = fmt.Sprintf("unknown(%d)", out.Mode)
			}
		case TLVQoSPriority:
			if len(t.Value) != 2 {
				return out, fmt.Errorf("escp: QoS priority entry has %d bytes, want 2", len(t.Value))
			}
			out.Priorities = append(out.Priorities, PortPriority{Port: int(t.Value[0]), Priority: int(t.Value[1]) + 1})
		}
	}
	if !seen {
		return out, fmt.Errorf("escp: no QoS mode in reply")
	}
	return out, nil
}

// QoS reads the QoS mode and per-port priorities. Priorities are only
// effective in port-based mode but are always reported.
func (c *Client) QoS(ctx context.Context) (QoS, error) {
	mode, err := c.getTLVs(ctx, TLVQoSMode)
	if err != nil {
		return QoS{}, err
	}
	pri, err := c.getTLVs(ctx, TLVQoSPriority)
	if err != nil {
		return QoS{}, err
	}
	return parseQoS(append(mode, pri...))
}

// SetQoSMode selects the QoS mode: 0 port-based, 1 802.1p, 2 DSCP/802.1p.
func (c *Client) SetQoSMode(ctx context.Context, mode int) error {
	if _, ok := QoSModeNames[mode]; !ok {
		return fmt.Errorf("escp: invalid QoS mode %d (valid: 0, 1, 2)", mode)
	}
	return c.setTLVs(ctx, TLV{TLVQoSMode, []byte{byte(mode)}})
}

// SetPortPriorities changes the priority (1 lowest .. 4 highest) of the given
// ports. As in the vendor utility the request carries the priority of every
// port, so the other ports are re-sent with their current value.
func (c *Client) SetPortPriorities(ctx context.Context, changes map[int]int) error {
	cur, err := c.QoS(ctx)
	if err != nil {
		return err
	}
	prio := map[int]int{}
	for _, p := range cur.Priorities {
		prio[p.Port] = p.Priority
	}
	for port, v := range changes {
		if _, ok := prio[port]; !ok {
			return fmt.Errorf("escp: port %d does not exist", port)
		}
		if v < 1 || v > 4 {
			return fmt.Errorf("escp: invalid priority %d for port %d (valid: 1-4)", v, port)
		}
		prio[port] = v
	}
	tlvs := make([]TLV, 0, len(prio))
	for _, p := range cur.Priorities { // keep the switch's port order
		tlvs = append(tlvs, TLV{TLVQoSPriority, []byte{byte(p.Port), byte(prio[p.Port] - 1)}})
	}
	return c.setTLVs(ctx, tlvs...)
}
