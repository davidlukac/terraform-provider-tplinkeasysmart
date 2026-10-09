package escp

import (
	"context"
	"fmt"
)

const TLVLAG = 4608

// LAG is one link aggregation group (Switching > Port Trunk).
type LAG struct {
	ID    int
	Ports []int // empty when the group is unused
}

// parseLAGs decodes TLV 4608: group id (1 byte), member port bitmap (4 bytes).
func parseLAGs(tlvs []TLV) ([]LAG, error) {
	var out []LAG
	for _, t := range tlvs {
		if t.Type != TLVLAG {
			continue
		}
		if len(t.Value) < 5 {
			return nil, fmt.Errorf("escp: LAG TLV too short (%d bytes)", len(t.Value))
		}
		out = append(out, LAG{ID: int(t.Value[0]), Ports: portsFromMask(t.Value[1:5])})
	}
	return out, nil
}

// LAGs reads the link aggregation groups.
func (c *Client) LAGs(ctx context.Context) ([]LAG, error) {
	tlvs, err := c.getTLVs(ctx, TLVLAG)
	if err != nil {
		return nil, err
	}
	return parseLAGs(tlvs)
}

// SetLAG creates or replaces a link aggregation group: TLV 4608 with the group
// id and the member port mask. An empty port list removes the group. The
// switch itself refuses ports that conflict with mirroring, VLANs, rate limits
// or storm control. Changing a group whose ports carry traffic can cut the
// uplink; callers must guard against that.
func (c *Client) SetLAG(ctx context.Context, group int, ports []int) error {
	if group < 1 || group > 255 {
		return fmt.Errorf("escp: invalid LAG group %d", group)
	}
	mask, err := maskFromPorts(ports)
	if err != nil {
		return err
	}
	return c.setTLVs(ctx, TLV{TLVLAG, append([]byte{byte(group)}, mask...)})
}
