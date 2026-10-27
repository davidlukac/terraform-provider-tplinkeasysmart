package escp

import (
	"context"
	"encoding/binary"
	"regexp"
	"strconv"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Limits of 802.1Q VLANs (VLAN > 802.1Q VLAN, VLAN > 802.1Q PVID Setting). The
// id range is the IEEE one; the name rules are those of the vendor utility
// (at most ten characters from letters, digits, hyphen and underscore).
const (
	MinVLANID      VLANID = 1
	MaxVLANID      VLANID = 4094
	MaxVLANNameLen        = 10

	// Layout of a 802.1Q VLAN entry (TLV 8705): id (2 bytes), member mask, tagged mask, name, NUL.
	vlan8021QOffMember = 2
	vlan8021QOffTagged = vlan8021QOffMember + maskLen
	vlan8021QOffName   = vlan8021QOffTagged + maskLen

	// pvidEntryLen is a PVID entry (TLV 8706): port (1 byte), pvid (2 bytes).
	pvidEntryLen = 3
)

var vlanNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]*$`)

// VLANID is the id of an 802.1Q VLAN, and the PVID of a port, which is the id of the VLAN its
// untagged traffic enters. The IEEE range is MinVLANID to MaxVLANID; two bytes carry it on the wire.
// (Port-based VLAN groups and LAG groups have ids of their own, one byte each, and are plain ints.)
type VLANID int

// Validate reports whether v is within MinVLANID to MaxVLANID; the error matches ErrInvalid.
func (v VLANID) Validate() error {
	if v < MinVLANID || v > MaxVLANID {
		return invalidf("escp: invalid VLAN id %d (valid: %d-%d)", v, MinVLANID, MaxVLANID)
	}
	return nil
}

// String is the id as a number.
func (v VLANID) String() string { return strconv.Itoa(int(v)) }

// VLANName is the name of an 802.1Q VLAN: empty, or up to MaxVLANNameLen letters, digits, hyphens and
// underscores (the vendor utility's rules).
type VLANName string

// Validate applies the rules; the error matches ErrInvalid.
func (n VLANName) Validate() error {
	if len(n) > MaxVLANNameLen || !vlanNamePattern.MatchString(string(n)) {
		return invalidf("escp: invalid VLAN name %q (at most %d letters, digits, hyphens or underscores)", string(n), MaxVLANNameLen)
	}
	return nil
}

// String is the name.
func (n VLANName) String() string { return string(n) }

// validate checks a VLAN before it is encoded: id, name, ports, no port both
// tagged and untagged, and at least one member (use DeleteVLAN8021Q to remove).
func (v VLAN8021Q) validate() error {
	if err := v.ID.Validate(); err != nil {
		return err
	}
	if err := v.Name.Validate(); err != nil {
		return err
	}
	if both := v.Tagged.Intersect(v.Untagged); !both.IsEmpty() {
		return invalidf("escp: port %d is both tagged and untagged in VLAN %d", both.Ports()[0], v.ID)
	}
	if v.Tagged.Union(v.Untagged).IsEmpty() {
		return invalidf("escp: VLAN %d has no member ports; delete it instead", v.ID)
	}
	return nil
}

// encode builds the TLV 8705 value for the VLAN, as the vendor utility does:
// the member mask holds every port, the tagged mask only the tagged ones.
func (v VLAN8021Q) encode() []byte {
	return vlanEntryBytes(v.ID, v.Tagged.Union(v.Untagged).Mask(), v.Tagged.Mask(), v.Name)
}

// vlanEntryBytes lays out a TLV 8705 value: id, member mask, tagged mask, name, NUL.
func vlanEntryBytes(id VLANID, member, tagged []byte, name VLANName) []byte {
	out := binary.BigEndian.AppendUint16(nil, uint16(id)) //nolint:gosec // callers validate the id (at most 4094)
	out = append(out, member...)
	out = append(out, tagged...)
	out = append(out, name...)
	return append(out, 0)
}

// SetVLAN8021Q creates or replaces an 802.1Q VLAN: TLV 8705 with the id, the
// member and tagged port masks and the name. Changing a VLAN whose ports carry
// traffic can cut connectivity; callers must guard. The 802.1Q mode must be on.
func (c *Client) SetVLAN8021Q(ctx context.Context, v VLAN8021Q) error {
	if err := v.validate(); err != nil {
		return err
	}
	members := v.Tagged.Union(v.Untagged)
	if err := c.requirePorts(ctx, members); err != nil {
		return err
	}
	return c.setTLVs(ctx, Operation{Name: "SetVLAN8021Q", Ports: members, Impact: ImpactTraffic, Replaces: GroupDot1QVLAN, ID: int(v.ID)}, wire.TLV{Type: wire.TLVVLAN8021QVLAN, Value: v.encode()})
}

// DeleteVLAN8021Q removes an 802.1Q VLAN: TLV 8705 with both masks empty and an
// empty name, as the vendor utility sends it.
func (c *Client) DeleteVLAN8021Q(ctx context.Context, id VLANID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	empty := make([]byte, maskLen)
	return c.setTLVs(ctx, Operation{Name: "DeleteVLAN8021Q", Impact: ImpactTraffic, Replaces: GroupDot1QVLAN, ID: int(id)}, wire.TLV{Type: wire.TLVVLAN8021QVLAN, Value: vlanEntryBytes(id, empty, empty, "")})
}

// SetPVIDs sets the default VLAN of the given ports (port -> pvid): one TLV
// 8706 per port with the port (1 byte) and the pvid (2 bytes). The VLAN must
// exist and the 802.1Q mode must be on; callers must guard linked ports.
func (c *Client) SetPVIDs(ctx context.Context, pvids map[PortID]VLANID) error {
	if len(pvids) == 0 {
		return invalidf("escp: no PVIDs to set")
	}
	var named PortSet
	for p, id := range pvids {
		if err := p.Validate(); err != nil {
			return err
		}
		if err := id.Validate(); err != nil {
			return err
		}
		named = named.Union(p.set())
	}
	if err := c.requirePorts(ctx, named); err != nil {
		return err
	}
	ports := named.Ports()
	tlvs := make([]wire.TLV, 0, len(ports))
	for _, p := range ports {
		value := binary.BigEndian.AppendUint16([]byte{byte(p)}, uint16(pvids[p])) //nolint:gosec // bounded by the validation above
		tlvs = append(tlvs, wire.TLV{Type: wire.TLVVLAN8021QPVID, Value: value})
	}
	return c.setTLVs(ctx, Operation{Name: "SetPVIDs", Ports: named, Impact: ImpactManagement}, tlvs...)
}
