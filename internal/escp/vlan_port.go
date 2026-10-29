package escp

import (
	"context"
	"fmt"
	"strconv"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Layout of a port-based VLAN entry (TLV 8449): VLAN id (1 byte), member port mask (maskLen bytes).
const (
	pvlanOffMask  = 1
	pvlanEntryLen = pvlanOffMask + maskLen
)

// MaxPortVLANID is the largest port-based VLAN id: the id is one byte on the wire.
const MaxPortVLANID = 255

// PortVLANID is the id of a port-based VLAN group, 1 to MaxPortVLANID. It is not an 802.1Q VLAN
// id (VLANID): the two modes have their own groups and their own ranges.
type PortVLANID int

// Validate reports whether g is an id the switch can hold; the error matches ErrInvalid.
func (g PortVLANID) Validate() error {
	if g < 1 || g > MaxPortVLANID {
		return invalidf("escp: invalid port-based VLAN id %d (valid: 1-%d)", g, MaxPortVLANID)
	}
	return nil
}

// String is the id as a number.
func (g PortVLANID) String() string { return strconv.Itoa(int(g)) }

// PortVLAN is one port-based VLAN group: the VLAN id (1 to MaxPortVLANID) and its member ports.
// Membership is exclusive: a port is in one group at a time.
type PortVLAN struct {
	ID    PortVLANID
	Ports PortSet
}

// PortBasedVLAN is the port-based VLAN state (VLAN > Port Based VLAN): whether this is the active
// VLAN mode, how many groups the switch supports, and the groups that exist.
type PortBasedVLAN struct {
	Enabled bool       // the port-based mode is the active VLAN mode
	MaxVLAN int        // the most groups the switch supports (0 when it did not say)
	VLANs   []PortVLAN // never nil
}

// PortVLANLimit is the highest port-based VLAN group id a switch holds: the larger of the number of
// groups it says it supports and its number of ports. Both are 8 on a TL-SG108E; the web UI takes
// new ids from 2 to the port count, and group 1 is the default group that always exists. Taking the
// larger of the two means a model whose group limit and port count differ (a 24-port TL-SG1024DE,
// say) is never refused an id one of them allows. The zero value is an unknown limit: only the
// wire's range (PortVLANID.Validate) applies.
type PortVLANLimit int

// NewPortVLANLimit is the limit of a switch that supports groups groups and has ports ports; either
// may be 0 when the switch did not say.
func NewPortVLANLimit(groups, ports int) PortVLANLimit { return PortVLANLimit(max(groups, ports)) }

// Known reports whether the switch said anything that bounds the ids.
func (l PortVLANLimit) Known() bool { return l > 0 }

// String is the limit as a number.
func (l PortVLANLimit) String() string { return strconv.Itoa(int(l)) }

// Check reports whether id is an id the switch can hold: within the wire's range and, when the limit
// is known, not above it. The error matches ErrInvalid.
func (l PortVLANLimit) Check(id PortVLANID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if l.Known() && int(id) > int(l) {
		return invalidf("escp: port-based VLAN id %d does not exist on this switch, which supports ids 1-%d", id, l)
	}
	return nil
}

// GroupLimit is the highest group id of the switch whose state v is and which has portCount ports
// (0 when unknown).
func (v PortBasedVLAN) GroupLimit(portCount int) PortVLANLimit {
	return NewPortVLANLimit(v.MaxVLAN, portCount)
}

// Group returns the member ports of group id and whether the group exists.
func (v PortBasedVLAN) Group(id PortVLANID) (PortSet, bool) {
	for _, g := range v.VLANs {
		if g.ID == id {
			return g.Ports, true
		}
	}
	return 0, false
}

func parsePortBasedVLAN(tlvs []wire.TLV) (PortBasedVLAN, error) {
	out := PortBasedVLAN{VLANs: []PortVLAN{}}
	seen := false
	for _, t := range tlvs {
		switch t.Type {
		case wire.TLVVLANPortBased:
			if len(t.Value) != 1 {
				return out, fmt.Errorf("escp: port-based VLAN state TLV has %d bytes", len(t.Value))
			}
			out.Enabled, seen = t.Value[0] == 1, true
		case wire.TLVVLANPortBasedMax:
			if len(t.Value) == 1 {
				out.MaxVLAN = int(t.Value[0])
			}
		case wire.TLVVLANPortBasedVID:
			if len(t.Value) != pvlanEntryLen {
				return out, fmt.Errorf("escp: port-based VLAN entry has %d bytes, want %d", len(t.Value), pvlanEntryLen)
			}
			out.VLANs = append(out.VLANs, PortVLAN{ID: PortVLANID(t.Value[0]), Ports: PortSetFromMask(t.Value[pvlanOffMask:])})
		}
	}
	if !seen {
		return out, fmt.Errorf("escp: no port-based VLAN state in reply")
	}
	return out, nil
}

// PortBasedVLAN reads the port-based VLAN mode and groups.
func (c *Client) PortBasedVLAN(ctx context.Context) (PortBasedVLAN, error) {
	tlvs, err := c.getTLVs(ctx, wire.TLVVLANPortBased)
	if err != nil {
		return PortBasedVLAN{}, err
	}
	return parsePortBasedVLAN(tlvs)
}

// SetPortBasedVLAN creates or replaces a port-based VLAN group: TLV 8449 with
// the VLAN id (1 byte) and the member port mask. An empty port set removes
// the group. The mode itself (enabled/disabled) is not touched. Changing a
// group whose ports carry traffic can cut connectivity; callers must guard.
func (c *Client) SetPortBasedVLAN(ctx context.Context, id PortVLANID, ports PortSet) error {
	if err := c.requirePortVLANGroup(ctx, id); err != nil {
		return err
	}
	if err := c.requirePorts(ctx, ports); err != nil {
		return err
	}
	return c.setTLVs(ctx, Operation{Name: "SetPortBasedVLAN", Ports: ports, Impact: ImpactTraffic, Replaces: GroupPortVLAN, ID: int(id)}, wire.TLV{Type: wire.TLVVLANPortBasedVID, Value: append([]byte{byte(id)}, ports.Mask()...)})
}

// requirePortVLANGroup refuses a group id the switch does not have. The limit is read once and kept,
// in the same way as the port count, so the first group write costs the reads of the group state
// and of the port count; if one fails, so does the write, with that error. The id's own range is
// checked first, before anything is read.
func (c *Client) requirePortVLANGroup(ctx context.Context, id PortVLANID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	c.mu.Lock()
	limit := c.groupLimit
	c.mu.Unlock()
	if !limit.Known() { // not read yet: a read always yields a known limit, as the port count must be reported
		v, err := c.PortBasedVLAN(ctx)
		if err != nil {
			return err
		}
		ports, err := c.portCount(ctx)
		if err != nil {
			return err
		}
		limit = v.GroupLimit(ports)
		c.mu.Lock()
		c.groupLimit = limit
		c.mu.Unlock()
	}
	return limit.Check(id)
}
