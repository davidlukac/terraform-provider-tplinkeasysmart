package escp

import (
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Layout of the mirror entry: enabled flag, destination port, ingress mask, egress mask.
const (
	mirrorOffEnabled = 0
	mirrorOffPort    = 1
	mirrorOffIngress = 2
	mirrorOffEgress  = mirrorOffIngress + maskLen
	mirrorEntryLen   = mirrorOffEgress + maskLen
)

// Mirror is the port mirroring configuration (Monitoring > Port Mirror): the traffic of the
// source ports is copied to MirrorPort. When Enabled is false the switch reports no ports.
type Mirror struct {
	Enabled      bool
	MirrorPort   PortID  // monitoring (destination) port, 0 when disabled
	IngressPorts PortSet // source ports whose incoming traffic is copied
	EgressPorts  PortSet // source ports whose outgoing traffic is copied
}

// parseMirror decodes TLV 16640: enabled, destination port, ingress mask(4), egress mask(4).
// When disabled the switch zeroes the rest, and so do we.
func parseMirror(tlvs []wire.TLV) (Mirror, error) {
	for _, t := range tlvs {
		if t.Type != wire.TLVMirror {
			continue
		}
		if len(t.Value) != mirrorEntryLen {
			return Mirror{}, fmt.Errorf("escp: port mirror TLV has %d bytes, want %d", len(t.Value), mirrorEntryLen)
		}
		m := Mirror{}
		if t.Value[mirrorOffEnabled] == 1 {
			m.Enabled, m.MirrorPort = true, PortID(t.Value[mirrorOffPort])
			m.IngressPorts = PortSetFromMask(t.Value[mirrorOffIngress:])
			m.EgressPorts = PortSetFromMask(t.Value[mirrorOffEgress:])
		}
		return m, nil
	}
	return Mirror{}, fmt.Errorf("escp: no port mirror TLV in reply")
}

// PortMirror reads the port mirroring configuration: whether it is on, the monitoring port and the
// source ports per direction.
func (c *Client) PortMirror(ctx context.Context) (Mirror, error) {
	tlvs, err := c.getTLVs(ctx, wire.TLVMirror)
	if err != nil {
		return Mirror{}, err
	}
	return parseMirror(tlvs)
}

// SetPortMirror writes the mirroring configuration (10-byte TLV 16640). To
// disable, pass the zero Mirror. The destination port stops carrying normal
// traffic; callers must guard against choosing a port that is in use.
func (c *Client) SetPortMirror(ctx context.Context, m Mirror) error {
	v := make([]byte, mirrorEntryLen)
	if m.Enabled {
		if err := m.MirrorPort.Validate(); err != nil {
			return err
		}
		sources := m.IngressPorts.Union(m.EgressPorts)
		if sources.IsEmpty() {
			return invalidf("escp: port mirroring needs at least one ingress or egress source port")
		}
		if sources.Has(m.MirrorPort) {
			return invalidf("escp: port %d cannot mirror itself", m.MirrorPort)
		}
		if err := c.requirePorts(ctx, sources.Union(m.MirrorPort.set())); err != nil {
			return err
		}
		v[mirrorOffEnabled], v[mirrorOffPort] = 1, byte(m.MirrorPort)
		copy(v[mirrorOffIngress:], m.IngressPorts.Mask())
		copy(v[mirrorOffEgress:], m.EgressPorts.Mask())
	} else if m.MirrorPort != 0 || !m.IngressPorts.IsEmpty() || !m.EgressPorts.IsEmpty() {
		return invalidf("escp: a disabled mirror must have no destination or source ports")
	}
	op := Operation{Name: "SetPortMirror", Impact: ImpactTraffic}
	if m.Enabled {
		op.Ports = m.IngressPorts.Union(m.EgressPorts).Union(m.MirrorPort.set())
	}
	return c.setTLVs(ctx, op, wire.TLV{Type: wire.TLVMirror, Value: v})
}
