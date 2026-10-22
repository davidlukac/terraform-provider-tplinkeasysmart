package escp

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Layout of a learned multicast group entry (TLV 4353): IPv4 address, VLAN id
// (2 bytes), port mask.
const (
	igmpOffIP    = 0
	igmpOffVLAN  = igmpOffIP + net.IPv4len
	igmpOffPorts = igmpOffVLAN + 2
	igmpGroupLen = igmpOffPorts + maskLen
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
func parseIGMP(tlvs []wire.TLV) (IGMP, error) {
	out := IGMP{Groups: []IGMPGroup{}}
	for _, t := range tlvs {
		switch t.Type {
		case wire.TLVIGMPEnabled:
			out.Enabled = len(t.Value) == 1 && t.Value[0] == 1
		case wire.TLVIGMPSuppression:
			out.ReportSuppression = len(t.Value) == 1 && t.Value[0] == 1
		case wire.TLVIGMPGroups:
			if len(t.Value)%igmpGroupLen != 0 {
				return out, fmt.Errorf("escp: IGMP group TLV length %d is not a multiple of %d", len(t.Value), igmpGroupLen)
			}
			for i := 0; i < len(t.Value); i += igmpGroupLen {
				e := t.Value[i : i+igmpGroupLen]
				out.Groups = append(out.Groups, IGMPGroup{
					IP: net.IP(e[igmpOffIP : igmpOffIP+net.IPv4len]).String(), VLAN: int(binary.BigEndian.Uint16(e[igmpOffVLAN:])), Ports: portsFromMask(e[igmpOffPorts:]),
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
	state, err := c.getTLVs(ctx, wire.TLVIGMPEnabled)
	if err != nil {
		return IGMP{}, err
	}
	groups, err := c.getTLVs(ctx, wire.TLVIGMPGroups)
	if err != nil {
		return IGMP{}, err
	}
	return parseIGMP(append(state, groups...))
}

// IGMPConfig is what SetIGMP writes. It is separate from IGMP (a reading) because the
// learned groups cannot be set.
type IGMPConfig struct {
	Enabled           bool
	ReportSuppression bool
}

// SetIGMP writes IGMP snooping state and report suppression together, as the vendor
// utility does, so a caller that changes one passes the other as read. With snooping off
// the switch floods multicast traffic to every port. Toggling it was verified on a live
// switch without interrupting traffic.
func (c *Client) SetIGMP(ctx context.Context, cfg IGMPConfig) error {
	return c.setTLVs(ctx,
		wire.TLV{Type: wire.TLVIGMPEnabled, Value: []byte{boolByte(cfg.Enabled)}},
		wire.TLV{Type: wire.TLVIGMPSuppression, Value: []byte{boolByte(cfg.ReportSuppression)}})
}
