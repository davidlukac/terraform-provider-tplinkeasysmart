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
// real TL-SG108E (firmware 20250710) and reading the code.
const (
	lagRefusedPortRange    int32 = 4    // ports outside the group's range
	lagRefusedPortVLAN     int32 = 7401 // a port is in a port-based VLAN other than the default
	lagRefusedBandwidth    int32 = 7403 // a port has a bandwidth limit
	lagRefusedStorm        int32 = 7404 // a port has storm control
	lagRefusedMirrorTarget int32 = 7205 // a port is the mirror destination
	lagRefusedMirrorSource int32 = 7206 // a port is mirrored
	lagRefusedSinglePort   int32 = 7415 // fewer than two ports
)

var lagRefusals = map[int32]string{
	lagRefusedPortRange:    "the ports do not fit the group: LAG 1 takes ports 1-4 and LAG 2 takes ports 5-8",
	lagRefusedPortVLAN:     "a member port belongs to a port-based VLAN other than the default one",
	lagRefusedBandwidth:    "a member port has a bandwidth limit",
	lagRefusedStorm:        "a member port has storm control enabled",
	lagRefusedMirrorTarget: "a member port is the destination of the port mirror",
	lagRefusedMirrorSource: "a member port is mirrored (a source of the port mirror)",
	lagRefusedSinglePort:   "a LAG needs at least two ports",
}

// ValidateLAG checks a group number and member ports against the limits
// above. An empty port list (removing the group) only needs a valid group.
func ValidateLAG(group int, ports []int) error {
	if group < 1 || group > LAGGroupCount {
		return fmt.Errorf("escp: invalid LAG group %d (valid: 1-%d)", group, LAGGroupCount)
	}
	if len(ports) == 0 {
		return nil
	}
	if len(ports) < LAGMinPorts || len(ports) > LAGPortsPerGroup {
		return fmt.Errorf("escp: a LAG takes %d to %d ports, got %d", LAGMinPorts, LAGPortsPerGroup, len(ports))
	}
	first, last := (group-1)*LAGPortsPerGroup+1, group*LAGPortsPerGroup
	for _, p := range ports {
		if p < first || p > last {
			return fmt.Errorf("escp: port %d cannot be in LAG %d (LAG %d takes ports %d-%d)", p, group, group, first, last)
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
// storm control; such a refusal comes back as a *RejectedError that names the
// conflict. Changing a group whose ports carry traffic can cut the uplink;
// callers must guard against that.
func (c *Client) SetLAG(ctx context.Context, group int, ports []int) error {
	mask, err := maskFromPorts(ports)
	if err != nil {
		return err
	}
	if err := ValidateLAG(group, ports); err != nil {
		return err
	}
	err = c.setTLVs(ctx, wire.TLV{Type: wire.TLVLAG, Value: append([]byte{byte(group)}, mask...)})
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		if reason, ok := lagRefusals[rejected.Code]; ok {
			return &RejectedError{Code: rejected.Code, Reason: reason}
		}
	}
	return err
}
