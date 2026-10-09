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
