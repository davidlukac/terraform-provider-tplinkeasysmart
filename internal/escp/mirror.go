package escp

import (
	"context"
	"fmt"
)

const TLVMirror = 16640

// Mirror is Monitoring > Port Mirror.
type Mirror struct {
	Enabled      bool
	MirrorPort   int // monitoring (destination) port, 0 when disabled
	IngressPorts []int
	EgressPorts  []int
}

// parseMirror decodes TLV 16640: enabled, destination port, ingress mask(4), egress mask(4).
// When disabled the switch zeroes the rest, and so do we.
func parseMirror(tlvs []TLV) (Mirror, error) {
	for _, t := range tlvs {
		if t.Type != TLVMirror {
			continue
		}
		if len(t.Value) != 10 {
			return Mirror{}, fmt.Errorf("escp: port mirror TLV has %d bytes, want 10", len(t.Value))
		}
		m := Mirror{IngressPorts: []int{}, EgressPorts: []int{}}
		if t.Value[0] == 1 {
			m.Enabled, m.MirrorPort = true, int(t.Value[1])
			m.IngressPorts, m.EgressPorts = portsFromMask(t.Value[2:6]), portsFromMask(t.Value[6:10])
		}
		return m, nil
	}
	return Mirror{}, fmt.Errorf("escp: no port mirror TLV in reply")
}

// PortMirror reads the port mirroring configuration.
func (c *Client) PortMirror(ctx context.Context) (Mirror, error) {
	tlvs, err := c.getTLVs(ctx, TLVMirror)
	if err != nil {
		return Mirror{}, err
	}
	return parseMirror(tlvs)
}
