package escp

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"
)

// TLV types of QoS > Bandwidth Control.
const (
	TLVBandwidthIngress = 12544 // one TLV per port: port(1) reserved(1) rate(4, kbps; 0 = unlimited)
	TLVBandwidthEgress  = 12545
)

// PortBandwidth holds a port's rate limits in kbps; 0 means unlimited.
type PortBandwidth struct {
	Port        int
	IngressKbps int
	EgressKbps  int
}

func parseBandwidth(tlvs []TLV) ([]PortBandwidth, error) {
	byPort := map[int]*PortBandwidth{}
	for _, t := range tlvs {
		if t.Type != TLVBandwidthIngress && t.Type != TLVBandwidthEgress {
			continue
		}
		if len(t.Value) != 6 {
			return nil, fmt.Errorf("escp: bandwidth entry has %d bytes, want 6", len(t.Value))
		}
		port, rate := int(t.Value[0]), int(binary.BigEndian.Uint32(t.Value[2:]))
		pb := byPort[port]
		if pb == nil {
			pb = &PortBandwidth{Port: port}
			byPort[port] = pb
		}
		if t.Type == TLVBandwidthIngress {
			pb.IngressKbps = rate
		} else {
			pb.EgressKbps = rate
		}
	}
	out := make([]PortBandwidth, 0, len(byPort))
	for _, pb := range byPort {
		out = append(out, *pb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out, nil
}

// Bandwidth reads the per-port ingress and egress rate limits.
func (c *Client) Bandwidth(ctx context.Context) ([]PortBandwidth, error) {
	in, err := c.getTLVs(ctx, TLVBandwidthIngress)
	if err != nil {
		return nil, err
	}
	eg, err := c.getTLVs(ctx, TLVBandwidthEgress)
	if err != nil {
		return nil, err
	}
	return parseBandwidth(append(in, eg...))
}
