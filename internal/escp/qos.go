package escp

import (
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// QoSMode is the switch's QoS mode. The number is the wire byte; the name is the web UI's
// and the Terraform provider's spelling and is part of the provider's state: do not change it.
type QoSMode int

// The QoS modes the switch knows. Per-port priorities only take effect in QoSPortBased.
const (
	QoSPortBased QoSMode = 0
	QoS8021p     QoSMode = 1
	QoSDSCP8021p QoSMode = 2
)

var qosModeNames = [...]string{QoSPortBased: "port-based", QoS8021p: "802.1p", QoSDSCP8021p: "dscp-802.1p"}

// QoSModes lists the modes SetQoSMode accepts, in the order of their codes.
func QoSModes() []QoSMode { return []QoSMode{QoSPortBased, QoS8021p, QoSDSCP8021p} }

// Valid reports whether the switch knows the mode.
func (m QoSMode) Valid() bool { return m >= 0 && int(m) < len(qosModeNames) }

// String returns the web UI's name of the mode, or unknown(n) for a code this client does not know.
func (m QoSMode) String() string {
	if !m.Valid() {
		return fmt.Sprintf("unknown(%d)", int(m))
	}
	return qosModeNames[m]
}

// ParseQoSMode converts a mode name as String spells it.
func ParseQoSMode(name string) (QoSMode, error) {
	for _, m := range QoSModes() {
		if m.String() == name {
			return m, nil
		}
	}
	return 0, invalidf("escp: unknown QoS mode %q (valid: %v)", name, QoSModes())
}

// Priority is a port's priority queue as the web UI shows it: PriorityLowest (1) to PriorityHighest (4).
// On the wire it is one less.
type Priority int

// The bounds of Priority.
const (
	PriorityLowest  Priority = 1
	PriorityHighest Priority = 4
)

// Valid reports whether the priority is within PriorityLowest..PriorityHighest.
func (p Priority) Valid() bool { return p >= PriorityLowest && p <= PriorityHighest }

// PortPriority is a port's priority queue.
type PortPriority struct {
	Port     int
	Priority Priority
}

// QoS is QoS > QoS Basic.
type QoS struct {
	Mode       QoSMode
	Priorities []PortPriority
}

func parseQoS(tlvs []wire.TLV) (QoS, error) {
	out := QoS{Priorities: []PortPriority{}}
	seen := false
	for _, t := range tlvs {
		switch t.Type {
		case wire.TLVQoSMode:
			if len(t.Value) != 1 {
				return out, fmt.Errorf("escp: QoS mode TLV has %d bytes", len(t.Value))
			}
			out.Mode, seen = QoSMode(t.Value[0]), true
		case wire.TLVQoSPriority:
			if len(t.Value) != 2 {
				return out, fmt.Errorf("escp: QoS priority entry has %d bytes, want 2", len(t.Value))
			}
			out.Priorities = append(out.Priorities, PortPriority{Port: int(t.Value[0]), Priority: Priority(t.Value[1]) + PriorityLowest})
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
	mode, err := c.getTLVs(ctx, wire.TLVQoSMode)
	if err != nil {
		return QoS{}, err
	}
	pri, err := c.getTLVs(ctx, wire.TLVQoSPriority)
	if err != nil {
		return QoS{}, err
	}
	return parseQoS(append(mode, pri...))
}

// SetQoSMode selects the QoS mode, which changes how traffic is queued on every port. The
// mode must be one of QoSModes. Per-port priorities (SetPortPriorities) only take effect in
// QoSPortBased; the switch accepts them in the other modes and ignores them.
func (c *Client) SetQoSMode(ctx context.Context, mode QoSMode) error {
	if !mode.Valid() {
		return invalidf("escp: invalid QoS mode %d (valid: 0, 1, 2)", int(mode))
	}
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVQoSMode, Value: []byte{byte(mode)}})
}

// SetPortPriorities changes the priority of the given ports (port -> priority). Every port
// must exist and every priority be within PriorityLowest..PriorityHighest, or nothing is
// sent. As in the vendor utility the request carries the priority of every port, so the
// current values are read first (a QoS read, which the write gate does not see) and the
// ports not mentioned are re-sent unchanged. The priorities only take effect in
// QoSPortBased mode.
func (c *Client) SetPortPriorities(ctx context.Context, changes map[int]Priority) error {
	cur, err := c.QoS(ctx)
	if err != nil {
		return err
	}
	prio := map[int]Priority{}
	for _, p := range cur.Priorities {
		prio[p.Port] = p.Priority
	}
	for port, v := range changes {
		if _, ok := prio[port]; !ok {
			return invalidf("escp: port %d does not exist", port)
		}
		if !v.Valid() {
			return invalidf("escp: invalid priority %d for port %d (valid: %d-%d)", int(v), port, int(PriorityLowest), int(PriorityHighest))
		}
		prio[port] = v
	}
	tlvs := make([]wire.TLV, 0, len(prio))
	for _, p := range cur.Priorities { // keep the switch's port order
		tlvs = append(tlvs, wire.TLV{Type: wire.TLVQoSPriority, Value: []byte{byte(p.Port), byte(prio[p.Port] - PriorityLowest)}})
	}
	return c.setTLVs(ctx, tlvs...)
}
