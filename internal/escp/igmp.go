package escp

import (
	"context"
	"fmt"
	"net"
)

const (
	TLVIGMPEnabled     = 4352
	TLVIGMPGroups      = 4353
	TLVIGMPSuppression = 4354
)

// IGMPGroup is one learned multicast group.
type IGMPGroup struct {
	IP    string
	VLAN  int
	Ports []int
}

// IGMP is the Switching > IGMP Snooping page.
type IGMP struct {
	Enabled           bool
	ReportSuppression bool
	Groups            []IGMPGroup
}

// parseIGMP decodes TLVs 4352 (enabled), 4354 (report message suppression)
// and 4353 (groups: repeated ip(4) vlan(2) port mask(4)).
func parseIGMP(tlvs []TLV) (IGMP, error) {
	out := IGMP{Groups: []IGMPGroup{}}
	for _, t := range tlvs {
		switch t.Type {
		case TLVIGMPEnabled:
			out.Enabled = len(t.Value) == 1 && t.Value[0] == 1
		case TLVIGMPSuppression:
			out.ReportSuppression = len(t.Value) == 1 && t.Value[0] == 1
		case TLVIGMPGroups:
			if len(t.Value)%10 != 0 {
				return out, fmt.Errorf("escp: IGMP group TLV length %d is not a multiple of 10", len(t.Value))
			}
			for i := 0; i < len(t.Value); i += 10 {
				e := t.Value[i : i+10]
				out.Groups = append(out.Groups, IGMPGroup{
					IP: net.IP(e[0:4]).String(), VLAN: int(e[4])<<8 | int(e[5]), Ports: portsFromMask(e[6:10]),
				})
			}
		}
	}
	return out, nil
}

// IGMP reads IGMP snooping state, report suppression and learned groups.
func (c *Client) IGMP(ctx context.Context) (IGMP, error) {
	// The switch rejects a combined request (error code 1); like the vendor
	// utility, ask for the state (which also returns 4354) and the groups apart.
	state, err := c.getTLVs(ctx, TLVIGMPEnabled)
	if err != nil {
		return IGMP{}, err
	}
	groups, err := c.getTLVs(ctx, TLVIGMPGroups)
	if err != nil {
		return IGMP{}, err
	}
	return parseIGMP(append(state, groups...))
}

// SetIGMP writes IGMP snooping state and report suppression together, as the vendor utility does.
func (c *Client) SetIGMP(ctx context.Context, enabled, suppression bool) error {
	return c.setTLVs(ctx,
		TLV{TLVIGMPEnabled, []byte{boolByte(enabled)}},
		TLV{TLVIGMPSuppression, []byte{boolByte(suppression)}})
}
