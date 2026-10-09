package escp

import (
	"context"
	"fmt"
)

// TLVVLANMTU is VLAN > MTU VLAN: 2 bytes (enabled, uplink port).
const TLVVLANMTU = 8192

// MTUVLAN is VLAN > MTU VLAN: when enabled, every port is isolated from the
// others and only talks to the uplink port.
type MTUVLAN struct {
	Enabled    bool
	UplinkPort int
}

// parseMTUVLAN decodes TLV 8192: enabled, uplink port. As in the vendor
// utility, a disabled MTU VLAN reports uplink port 1.
func parseMTUVLAN(tlvs []TLV) (MTUVLAN, error) {
	for _, t := range tlvs {
		if t.Type == TLVVLANMTU && len(t.Value) == 2 {
			m := MTUVLAN{Enabled: t.Value[0] == 1, UplinkPort: int(t.Value[1])}
			if !m.Enabled {
				m.UplinkPort = 1
			}
			return m, nil
		}
	}
	return MTUVLAN{}, fmt.Errorf("escp: no valid MTU VLAN TLV in reply")
}

// MTUVLAN reads the MTU VLAN mode.
func (c *Client) MTUVLAN(ctx context.Context) (MTUVLAN, error) {
	tlvs, err := c.getTLVs(ctx, TLVVLANMTU)
	if err != nil {
		return MTUVLAN{}, err
	}
	return parseMTUVLAN(tlvs)
}
