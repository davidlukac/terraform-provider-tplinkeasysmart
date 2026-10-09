package escp

import (
	"context"
	"fmt"
)

// TLVMirror is Monitoring > Port Mirror: 10 bytes (enabled, destination port, ingress mask, egress mask).
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

func maskFromPorts(ports []int) ([]byte, error) {
	var m uint32
	for _, p := range ports {
		if p < 1 || p > 32 {
			return nil, fmt.Errorf("escp: invalid port %d", p)
		}
		m |= 1 << uint(p-1)
	}
	return []byte{byte(m >> 24), byte(m >> 16), byte(m >> 8), byte(m)}, nil
}

// SetPortMirror writes the mirroring configuration (10-byte TLV 16640). To
// disable, pass the zero Mirror. The destination port stops carrying normal
// traffic; callers must guard against choosing a port that is in use.
func (c *Client) SetPortMirror(ctx context.Context, m Mirror) error {
	v := make([]byte, 10)
	if m.Enabled {
		if m.MirrorPort < 1 || m.MirrorPort > 32 {
			return fmt.Errorf("escp: invalid mirror port %d", m.MirrorPort)
		}
		if len(m.IngressPorts)+len(m.EgressPorts) == 0 {
			return fmt.Errorf("escp: port mirroring needs at least one ingress or egress source port")
		}
		for _, p := range append(append([]int{}, m.IngressPorts...), m.EgressPorts...) {
			if p == m.MirrorPort {
				return fmt.Errorf("escp: port %d cannot mirror itself", p)
			}
		}
		in, err := maskFromPorts(m.IngressPorts)
		if err != nil {
			return err
		}
		eg, err := maskFromPorts(m.EgressPorts)
		if err != nil {
			return err
		}
		v[0], v[1] = 1, byte(m.MirrorPort)
		copy(v[2:6], in)
		copy(v[6:10], eg)
	} else if m.MirrorPort != 0 || len(m.IngressPorts) != 0 || len(m.EgressPorts) != 0 {
		return fmt.Errorf("escp: a disabled mirror must have no destination or source ports")
	}
	return c.setTLVs(ctx, TLV{TLVMirror, v})
}
