package escp

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Layout of a LAG entry (TLV 4608): group id (1 byte), member port mask (maskLen bytes).
const (
	lagOffMask  = 1
	lagEntryLen = lagOffMask + maskLen
)

// LAG limits of the TL-SG108E. They come from the switch's own web UI
// (PortTrunkRpm.htm: "available ports of LAG 1: port 1 -- port 4", "LAG 2:
// port 5 -- port 8", at most 4 ports, at least 2) and were confirmed on the
// wire on firmware 20230218 and 20250710: a mask outside the group's range is
// refused with code 4.
const (
	// LAGGroupCount is the number of LAG groups.
	LAGGroupCount = 2
	// LAGPortsPerGroup is how many ports each group owns (group 1: ports 1-4, group 2: 5-8) and the most it can hold.
	LAGPortsPerGroup = 4
	// LAGMinPorts is the fewest members a group can have (a single port is refused with code 7415).
	LAGMinPorts = 2
)

// Reply codes of a refused LAG write, found by provoking each conflict on a
// real TL-SG108E (firmware 20250710) and reading the code. The member rules
// (speed, duplex, negotiation, flow control, enabled state, QoS priority) are the
// ones the vendor utility checks before it sends the write; the switch enforces
// them itself when the LAG is created.
const (
	lagRefusedPortRange    int32 = 4    // ports outside the group's range
	lagRefusedPortVLAN     int32 = 7401 // a port is in a port-based VLAN other than the default
	lagRefusedBandwidth    int32 = 7403 // a port has a bandwidth limit
	lagRefusedStorm        int32 = 7404 // a port has storm control
	lagRefusedQoS          int32 = 7405 // member ports have different QoS priorities (checked in port-based QoS mode only)
	lagRefusedSpeed        int32 = 7406 // member ports have different fixed speeds
	lagRefusedHalfDuplex   int32 = 7407 // a member port is set to half duplex (also refused when a port write would make a member half duplex)
	lagRefusedNegotiation  int32 = 7410 // member ports mix auto-negotiation with a fixed speed
	lagRefusedSinglePort   int32 = 7415 // fewer than two ports
	lagRefusedFlowControl  int32 = 7417 // member ports have different flow control settings
	lagRefusedEnabled      int32 = 7418 // member ports are not all enabled or all disabled
	lagRefusedMirrorTarget int32 = 7205 // a port is the mirror destination
	lagRefusedMirrorSource int32 = 7206 // a port is mirrored
)

var lagRefusals = map[int32]string{
	lagRefusedPortRange:    "the ports do not fit the group: LAG 1 takes ports 1-4 and LAG 2 takes ports 5-8",
	lagRefusedPortVLAN:     "a member port belongs to a port-based VLAN other than the default one",
	lagRefusedBandwidth:    "a member port has a bandwidth limit",
	lagRefusedStorm:        "a member port has storm control enabled",
	lagRefusedQoS:          "the member ports have different QoS priorities",
	lagRefusedSpeed:        "the member ports do not have the same speed",
	lagRefusedHalfDuplex:   "a member port is set to half duplex; LAG members must be full duplex",
	lagRefusedNegotiation:  "the member ports mix auto-negotiation with a fixed speed",
	lagRefusedMirrorTarget: "a member port is the destination of the port mirror",
	lagRefusedMirrorSource: "a member port is mirrored (a source of the port mirror)",
	lagRefusedSinglePort:   "a LAG needs at least two ports",
	lagRefusedFlowControl:  "the member ports do not have the same flow control setting",
	lagRefusedEnabled:      "the member ports are not all enabled or all disabled",
}

// ValidateLAG checks a group number and member ports against the limits
// above. An empty port list (removing the group) only needs a valid group.
func ValidateLAG(group int, ports []int) error {
	if group < 1 || group > LAGGroupCount {
		return invalidf("escp: invalid LAG group %d (valid: 1-%d)", group, LAGGroupCount)
	}
	if len(ports) == 0 {
		return nil
	}
	if len(ports) < LAGMinPorts || len(ports) > LAGPortsPerGroup {
		return invalidf("escp: a LAG takes %d to %d ports, got %d", LAGMinPorts, LAGPortsPerGroup, len(ports))
	}
	first, last := (group-1)*LAGPortsPerGroup+1, group*LAGPortsPerGroup
	for _, p := range ports {
		if p < first || p > last {
			return invalidf("escp: port %d cannot be in LAG %d (LAG %d takes ports %d-%d)", p, group, group, first, last)
		}
	}
	return nil
}

// LAG is one link aggregation group (Switching > Port Trunk).
type LAG struct {
	ID    int
	Ports []int // empty when the group is unused
}

// parseLAGs decodes TLV 4608: group id (1 byte), member port bitmap (4 bytes).
func parseLAGs(tlvs []wire.TLV) ([]LAG, error) {
	var out []LAG
	for _, t := range tlvs {
		if t.Type != wire.TLVLAG {
			continue
		}
		if len(t.Value) < lagEntryLen {
			return nil, fmt.Errorf("escp: LAG TLV too short (%d bytes)", len(t.Value))
		}
		out = append(out, LAG{ID: int(t.Value[0]), Ports: portsFromMask(t.Value[lagOffMask:])})
	}
	return out, nil
}

// LAGs reads the link aggregation groups.
func (c *Client) LAGs(ctx context.Context) ([]LAG, error) {
	tlvs, err := c.getTLVs(ctx, wire.TLVLAG)
	if err != nil {
		return nil, err
	}
	return parseLAGs(tlvs)
}

// SetLAG creates or replaces a link aggregation group: TLV 4608 with the group
// id and the member port mask. An empty port list removes the group. Group
// and ports are checked against ValidateLAG first. The switch itself refuses
// ports that conflict with mirroring, a port-based VLAN, a bandwidth limit or
// storm control, and member ports that differ in speed, negotiation, flow control,
// enabled state or (in port-based QoS mode) QoS priority, or that are half duplex;
// such a refusal comes back as a *RejectedError that names the conflict. The switch
// checks these only when the group is written: it does not keep members alike
// afterwards (see SetPort). Changing a group whose ports carry traffic can cut the
// uplink; callers must guard against that.
func (c *Client) SetLAG(ctx context.Context, group int, ports []int) error {
	mask, err := maskFromPorts(ports)
	if err != nil {
		return err
	}
	if err := ValidateLAG(group, ports); err != nil {
		return err
	}
	if err := c.requirePorts(ctx, ports...); err != nil {
		return err
	}
	return nameRefusal(c.setTLVs(ctx, wire.TLV{Type: wire.TLVLAG, Value: append([]byte{byte(group)}, mask...)}), lagRefusals)
}

// nameRefusal adds the reason to a *RejectedError whose code is in reasons; any other
// error, and a refusal with an unknown code, is returned as it is.
func nameRefusal(err error, reasons map[int32]string) error {
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		if reason, ok := reasons[rejected.Code]; ok {
			return &RejectedError{Code: rejected.Code, Reason: reason}
		}
	}
	return err
}
