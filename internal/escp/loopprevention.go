package escp

import (
	"context"
	"fmt"
)

const TLVLoopPrevention = 17152

// LoopPrevention reports Monitoring > Loop Prevention.
func (c *Client) LoopPrevention(ctx context.Context) (bool, error) {
	tlvs, err := c.getTLVs(ctx, TLVLoopPrevention)
	if err != nil {
		return false, err
	}
	return parseLoopPrevention(tlvs)
}

func parseLoopPrevention(tlvs []TLV) (bool, error) {
	for _, t := range tlvs {
		if t.Type == TLVLoopPrevention && len(t.Value) == 1 {
			return t.Value[0] == 1, nil
		}
	}
	return false, fmt.Errorf("escp: no loop prevention TLV in reply")
}

// SetLoopPrevention enables or disables loop prevention.
func (c *Client) SetLoopPrevention(ctx context.Context, on bool) error {
	return c.setTLVs(ctx, TLV{TLVLoopPrevention, []byte{boolByte(on)}})
}
