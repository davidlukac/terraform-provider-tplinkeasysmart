package escp

import (
	"context"
	"fmt"
)

// TLV types of VLAN > Port Based VLAN.
const (
	TLVVLANPortBased    = 8448 // mode enabled (1 byte)
	TLVVLANPortBasedVID = 8449 // one TLV per VLAN: id (1 byte), member port mask (4 bytes)
	TLVVLANPortBasedMax = 8450 // maximum number of VLANs (1 byte)
)

// Layout of a port-based VLAN entry (TLV 8449): VLAN id (1 byte), member port mask (maskLen bytes).
const (
	pvlanOffMask  = 1
	pvlanEntryLen = pvlanOffMask + maskLen
)

// PortVLAN is one port-based VLAN group.
type PortVLAN struct {
	ID    int
	Ports []int
}

// PortBasedVLAN is VLAN > Port Based VLAN.
type PortBasedVLAN struct {
	Enabled bool
	MaxVLAN int
	VLANs   []PortVLAN
}

func parsePortBasedVLAN(tlvs []TLV) (PortBasedVLAN, error) {
	out := PortBasedVLAN{VLANs: []PortVLAN{}}
	seen := false
	for _, t := range tlvs {
		switch t.Type {
		case TLVVLANPortBased:
			if len(t.Value) != 1 {
				return out, fmt.Errorf("escp: port-based VLAN state TLV has %d bytes", len(t.Value))
			}
			out.Enabled, seen = t.Value[0] == 1, true
		case TLVVLANPortBasedMax:
			if len(t.Value) == 1 {
				out.MaxVLAN = int(t.Value[0])
			}
		case TLVVLANPortBasedVID:
			if len(t.Value) != pvlanEntryLen {
				return out, fmt.Errorf("escp: port-based VLAN entry has %d bytes, want %d", len(t.Value), pvlanEntryLen)
			}
			out.VLANs = append(out.VLANs, PortVLAN{ID: int(t.Value[0]), Ports: portsFromMask(t.Value[pvlanOffMask:])})
		}
	}
	if !seen {
		return out, fmt.Errorf("escp: no port-based VLAN state in reply")
	}
	return out, nil
}

// PortBasedVLAN reads the port-based VLAN mode and groups.
func (c *Client) PortBasedVLAN(ctx context.Context) (PortBasedVLAN, error) {
	tlvs, err := c.getTLVs(ctx, TLVVLANPortBased)
	if err != nil {
		return PortBasedVLAN{}, err
	}
	return parsePortBasedVLAN(tlvs)
}

// SetPortBasedVLAN creates or replaces a port-based VLAN group: TLV 8449 with
// the VLAN id (1 byte) and the member port mask. An empty port list removes
// the group. The mode itself (enabled/disabled) is not touched. Changing a
// group whose ports carry traffic can cut connectivity; callers must guard.
func (c *Client) SetPortBasedVLAN(ctx context.Context, id int, ports []int) error {
	if err := validateByteID("port-based VLAN id", id); err != nil {
		return err
	}
	mask, err := maskFromPorts(ports)
	if err != nil {
		return err
	}
	return c.setTLVs(ctx, TLV{TLVVLANPortBasedVID, append([]byte{byte(id)}, mask...)})
}
