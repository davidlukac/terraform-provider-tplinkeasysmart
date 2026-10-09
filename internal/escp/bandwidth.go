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

// MaxBandwidthKbps is the largest limit the web UI offers.
const MaxBandwidthKbps = 1000000

func bandwidthTLV(typ int16, port, kbps int) (TLV, error) {
	if kbps < 0 || kbps > MaxBandwidthKbps || kbps%64 != 0 {
		return TLV{}, fmt.Errorf("escp: invalid rate %d kbps for port %d: use 0 (unlimited) or a multiple of 64 up to %d", kbps, port, MaxBandwidthKbps)
	}
	v := make([]byte, 6)
	v[0] = byte(port)
	if kbps != 0 {
		v[1] = 1
	}
	binary.BigEndian.PutUint32(v[2:], uint32(kbps))
	return TLV{typ, v}, nil
}

// SetBandwidth sets a port's ingress and egress limits in kbps (0 = unlimited,
// otherwise a multiple of 64). Like the vendor utility it sends two requests,
// ingress first, then egress. A limit on a port that carries traffic throttles
// it; callers must guard against that.
func (c *Client) SetBandwidth(ctx context.Context, port, ingressKbps, egressKbps int) error {
	if port < 1 || port > 32 {
		return fmt.Errorf("escp: invalid port %d", port)
	}
	in, err := bandwidthTLV(TLVBandwidthIngress, port, ingressKbps)
	if err != nil {
		return err
	}
	eg, err := bandwidthTLV(TLVBandwidthEgress, port, egressKbps)
	if err != nil {
		return err
	}
	if err := c.setTLVs(ctx, in); err != nil {
		return err
	}
	return c.setTLVs(ctx, eg)
}
