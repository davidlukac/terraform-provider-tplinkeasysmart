package escp

import (
	"context"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// VLANMode is which of the switch's three VLAN modes is active. They exclude each other: the
// vendor utility warns that switching discards the settings of the other modes. The values
// are the spellings the Terraform provider keeps in its state, so they do not change.
type VLANMode string

// The VLAN modes. VLANModeNone is what VLANMode reports for a switch with no mode on, or with
// several on (which the firmware should not allow); it cannot be written.
const (
	VLANModeNone      VLANMode = "none"
	VLANModeMTU       VLANMode = "mtu"
	VLANModePortBased VLANMode = "port-based"
	VLANModeDot1Q     VLANMode = "802.1q"
)

// VLANModes lists the modes SetVLANMode accepts.
func VLANModes() []VLANMode { return []VLANMode{VLANModeMTU, VLANModePortBased, VLANModeDot1Q} }

// VLANModeSetting is the active VLAN mode and, for the MTU VLAN, its uplink port (zero for
// every other mode).
type VLANModeSetting struct {
	Mode       VLANMode
	UplinkPort PortID
}

// VLANModeFrom names the active mode from the three mode flags the switch reports. No flag, or
// more than one, is VLANModeNone: a state the client cannot write, shown as it is instead of
// being hidden.
func VLANModeFrom(mtu MTUVLAN, portBased, dot1q bool) VLANModeSetting {
	active, st := 0, VLANModeSetting{Mode: VLANModeNone}
	if mtu.Enabled {
		active, st = active+1, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: mtu.UplinkPort}
	}
	if portBased {
		active, st = active+1, VLANModeSetting{Mode: VLANModePortBased}
	}
	if dot1q {
		active, st = active+1, VLANModeSetting{Mode: VLANModeDot1Q}
	}
	if active != 1 {
		return VLANModeSetting{Mode: VLANModeNone}
	}
	return st
}

// VLANMode reads the three mode flags (three reads) and names the active mode.
func (c *Client) VLANMode(ctx context.Context) (VLANModeSetting, error) {
	mtu, err := c.MTUVLAN(ctx)
	if err != nil {
		return VLANModeSetting{}, err
	}
	pb, err := c.PortBasedVLAN(ctx)
	if err != nil {
		return VLANModeSetting{}, err
	}
	q, err := c.Dot1Q(ctx)
	if err != nil {
		return VLANModeSetting{}, err
	}
	return VLANModeFrom(mtu, pb.Enabled, q.Enabled), nil
}

// ValidateVLANMode checks a setting for SetVLANMode: one of VLANModes; for the MTU VLAN a
// valid uplink port, for the others none.
func ValidateVLANMode(s VLANModeSetting) error {
	switch s.Mode {
	case VLANModeMTU:
		return s.UplinkPort.Validate()
	case VLANModePortBased, VLANModeDot1Q:
		if s.UplinkPort != 0 {
			return invalidf("escp: VLAN mode %s has no uplink port (got %d)", s.Mode, s.UplinkPort)
		}
		return nil
	default:
		return invalidf("escp: VLAN mode %q cannot be set (valid: %v)", s.Mode, VLANModes())
	}
}

// SetVLANMode makes the given VLAN mode the active one; the switch turns the other two off and
// discards their settings, which can cut every connection, including the management path.
// Callers must guard. With the MTU VLAN every port can only talk to the uplink port. Writing
// the mode the switch already has is harmless but not skipped: compare with VLANMode first.
func (c *Client) SetVLANMode(ctx context.Context, s VLANModeSetting) error {
	if err := ValidateVLANMode(s); err != nil {
		return err
	}
	var tlv wire.TLV
	switch s.Mode {
	case VLANModeMTU:
		if err := c.requirePorts(ctx, s.UplinkPort.set()); err != nil {
			return err
		}
		tlv = wire.TLV{Type: wire.TLVVLANMTU, Value: []byte{boolByte(true), byte(s.UplinkPort)}}
	case VLANModePortBased:
		tlv = wire.TLV{Type: wire.TLVVLANPortBased, Value: []byte{boolByte(true)}}
	default: // VLANModeDot1Q: ValidateVLANMode refused every other mode
		tlv = wire.TLV{Type: wire.TLVVLAN8021Q, Value: []byte{boolByte(true)}}
	}
	op := Operation{Name: "SetVLANMode", Impact: ImpactManagement}
	if s.Mode == VLANModeMTU {
		op.Ports = s.UplinkPort.set()
	}
	return c.setTLVs(ctx, op, tlv)
}
