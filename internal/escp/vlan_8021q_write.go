package escp

import (
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"sort"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Limits of 802.1Q VLANs (VLAN > 802.1Q VLAN, VLAN > 802.1Q PVID Setting). The
// id range is the IEEE one; the name rules are those of the vendor utility
// (at most ten characters from letters, digits, hyphen and underscore).
const (
	MinVLANID      = 1
	MaxVLANID      = 4094
	MaxVLANNameLen = 10

	// Layout of a 802.1Q VLAN entry (TLV 8705): id (2 bytes), member mask, tagged mask, name, NUL.
	vlan8021QOffMember = 2
	vlan8021QOffTagged = vlan8021QOffMember + maskLen
	vlan8021QOffName   = vlan8021QOffTagged + maskLen

	// pvidEntryLen is a PVID entry (TLV 8706): port (1 byte), pvid (2 bytes).
	pvidEntryLen = 3

	// mtuDisabledUplink is the uplink port sent when the MTU VLAN is switched off (the switch reports 1 then).
	mtuDisabledUplink = 1
)

// VLANNamePattern is what a VLAN name may consist of (see ValidateVLANName).
const VLANNamePattern = `^[A-Za-z0-9_-]*$`

var vlanNamePattern = regexp.MustCompile(VLANNamePattern)

// ValidateVLANID checks an 802.1Q VLAN id or PVID.
func ValidateVLANID(id int) error {
	if id < MinVLANID || id > MaxVLANID {
		return fmt.Errorf("escp: invalid VLAN id %d (valid: %d-%d)", id, MinVLANID, MaxVLANID)
	}
	return nil
}

// ValidateVLANName checks an 802.1Q VLAN name: empty, or up to MaxVLANNameLen letters, digits, hyphens and underscores.
func ValidateVLANName(name string) error {
	if len(name) > MaxVLANNameLen || !vlanNamePattern.MatchString(name) {
		return fmt.Errorf("escp: invalid VLAN name %q (at most %d letters, digits, hyphens or underscores)", name, MaxVLANNameLen)
	}
	return nil
}

// validate checks a VLAN before it is encoded: id, name, ports, no port both
// tagged and untagged, and at least one member (use DeleteVLAN8021Q to remove).
func (v VLAN8021Q) validate() error {
	if err := ValidateVLANID(v.ID); err != nil {
		return err
	}
	if err := ValidateVLANName(v.Name); err != nil {
		return err
	}
	seen := map[int]bool{}
	for _, p := range v.Untagged {
		seen[p] = true
	}
	for _, p := range v.Tagged {
		if seen[p] {
			return fmt.Errorf("escp: port %d is both tagged and untagged in VLAN %d", p, v.ID)
		}
	}
	if len(v.Tagged)+len(v.Untagged) == 0 {
		return fmt.Errorf("escp: VLAN %d has no member ports; delete it instead", v.ID)
	}
	return nil
}

// encode builds the TLV 8705 value for the VLAN, as the vendor utility does:
// the member mask holds every port, the tagged mask only the tagged ones.
func (v VLAN8021Q) encode() ([]byte, error) {
	untagged, err := maskFromPorts(v.Untagged)
	if err != nil {
		return nil, err
	}
	tagged, err := maskFromPorts(v.Tagged)
	if err != nil {
		return nil, err
	}
	member := binary.BigEndian.AppendUint32(nil, binary.BigEndian.Uint32(untagged)|binary.BigEndian.Uint32(tagged))
	return vlanEntryBytes(v.ID, member, tagged, v.Name), nil
}

// vlanEntryBytes lays out a TLV 8705 value: id, member mask, tagged mask, name, NUL.
func vlanEntryBytes(id int, member, tagged []byte, name string) []byte {
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
	value, err := v.encode()
	if err != nil {
		return err
	}
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVVLAN8021QVLAN, Value: value})
}

// DeleteVLAN8021Q removes an 802.1Q VLAN: TLV 8705 with both masks empty and an
// empty name, as the vendor utility sends it.
func (c *Client) DeleteVLAN8021Q(ctx context.Context, id int) error {
	if err := ValidateVLANID(id); err != nil {
		return err
	}
	empty := make([]byte, maskLen)
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVVLAN8021QVLAN, Value: vlanEntryBytes(id, empty, empty, "")})
}

// SetPVIDs sets the default VLAN of the given ports (port -> pvid): one TLV
// 8706 per port with the port (1 byte) and the pvid (2 bytes). The VLAN must
// exist and the 802.1Q mode must be on; callers must guard linked ports.
func (c *Client) SetPVIDs(ctx context.Context, pvids map[int]int) error {
	if len(pvids) == 0 {
		return fmt.Errorf("escp: no PVIDs to set")
	}
	ports := make([]int, 0, len(pvids))
	for p, id := range pvids {
		if err := ValidatePort(p); err != nil {
			return err
		}
		if err := ValidateVLANID(id); err != nil {
			return err
		}
		ports = append(ports, p)
	}
	sort.Ints(ports)
	tlvs := make([]wire.TLV, 0, len(ports))
	for _, p := range ports {
		value := binary.BigEndian.AppendUint16([]byte{byte(p)}, uint16(pvids[p])) //nolint:gosec // bounded by the validation above
		tlvs = append(tlvs, wire.TLV{Type: wire.TLVVLAN8021QPVID, Value: value})
	}
	return c.setTLVs(ctx, tlvs...)
}

// SetVLAN8021QMode switches the 802.1Q VLAN mode on or off: TLV 8704. The
// vendor utility warns that the VLAN modes are mutually exclusive, so turning
// it on discards the settings of the other modes. Callers must guard.
func (c *Client) SetVLAN8021QMode(ctx context.Context, enabled bool) error {
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVVLAN8021Q, Value: []byte{boolByte(enabled)}})
}

// SetPortBasedVLANMode switches the port-based VLAN mode on or off: TLV 8448.
// Same warning as SetVLAN8021QMode.
func (c *Client) SetPortBasedVLANMode(ctx context.Context, enabled bool) error {
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVVLANPortBased, Value: []byte{boolByte(enabled)}})
}

// SetMTUVLAN switches the MTU VLAN mode on or off: TLV 8192 with the enabled
// flag and the uplink port. With the mode on, every port can only talk to the
// uplink port. Same warning as SetVLAN8021QMode.
func (c *Client) SetMTUVLAN(ctx context.Context, enabled bool, uplinkPort int) error {
	if !enabled {
		uplinkPort = mtuDisabledUplink
	}
	if err := ValidatePort(uplinkPort); err != nil {
		return err
	}
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVVLANMTU, Value: []byte{boolByte(enabled), byte(uplinkPort)}})
}
