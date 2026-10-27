package escp

import (
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// VLAN8021Q is one 802.1Q VLAN with its tagged and untagged members.
type VLAN8021Q struct {
	ID       int
	Name     string
	Tagged   PortSet
	Untagged PortSet
}

// PortPVID is the default VLAN of a port.
type PortPVID struct {
	Port PortID
	PVID int
}

// Dot1Q is VLAN > 802.1Q VLAN plus PVID settings.
type Dot1Q struct {
	Enabled bool
	MaxVLAN int
	VLANs   []VLAN8021Q
	PVIDs   []PortPVID
}

func parseDot1Q(tlvs []wire.TLV) (Dot1Q, error) {
	out := Dot1Q{VLANs: []VLAN8021Q{}, PVIDs: []PortPVID{}}
	seen := false
	for _, t := range tlvs {
		v := t.Value
		switch t.Type {
		case wire.TLVVLAN8021Q:
			if len(v) != 1 {
				return out, fmt.Errorf("escp: 802.1Q state TLV has %d bytes", len(v))
			}
			out.Enabled, seen = v[0] == 1, true
		case wire.TLVVLAN8021QMax:
			if len(v) == 1 {
				out.MaxVLAN = int(v[0])
			}
		case wire.TLVVLAN8021QVLAN:
			if len(v) < vlan8021QOffName {
				return out, fmt.Errorf("escp: 802.1Q VLAN entry has %d bytes, want at least %d", len(v), vlan8021QOffName)
			}
			// Vendor semantics: member and tagged-mask -> tagged; member only -> untagged.
			members, taggedMask := PortSetFromMask(v[vlan8021QOffMember:]), PortSetFromMask(v[vlan8021QOffTagged:])
			untagged := members.Minus(taggedMask)
			out.VLANs = append(out.VLANs, VLAN8021Q{ID: int(v[0])<<8 | int(v[1]), Name: wire.CString(v[vlan8021QOffName:]),
				Tagged: members.Minus(untagged), Untagged: untagged})
		case wire.TLVVLAN8021QPVID:
			if len(v) != pvidEntryLen {
				return out, fmt.Errorf("escp: 802.1Q PVID entry has %d bytes, want %d", len(v), pvidEntryLen)
			}
			out.PVIDs = append(out.PVIDs, PortPVID{Port: PortID(v[0]), PVID: int(v[1])<<8 | int(v[2])})
		}
	}
	if !seen {
		return out, fmt.Errorf("escp: no 802.1Q state in reply")
	}
	return out, nil
}

// Dot1Q reads the 802.1Q mode, VLAN table and per-port PVIDs.
func (c *Client) Dot1Q(ctx context.Context) (Dot1Q, error) {
	// Separate requests, like the vendor utility (combined ones are rejected on some pages).
	vlans, err := c.getTLVs(ctx, wire.TLVVLAN8021QVLAN)
	if err != nil {
		return Dot1Q{}, err
	}
	pvids, err := c.getTLVs(ctx, wire.TLVVLAN8021QPVID)
	if err != nil {
		return Dot1Q{}, err
	}
	return parseDot1Q(append(vlans, pvids...))
}
