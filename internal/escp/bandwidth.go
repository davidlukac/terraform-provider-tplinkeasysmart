package escp

import (
	"context"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// PortBandwidth holds a port's rate limits in kbps; 0 means unlimited.
type PortBandwidth struct {
	Port        int
	IngressKbps int
	EgressKbps  int
}

func parseBandwidth(tlvs []wire.TLV) ([]PortBandwidth, error) {
	byPort := map[int]*PortBandwidth{}
	for _, t := range tlvs {
		if t.Type != wire.TLVBandwidthIngress && t.Type != wire.TLVBandwidthEgress {
			continue
		}
		if len(t.Value) != bwEntryLen {
			return nil, fmt.Errorf("escp: bandwidth entry has %d bytes, want %d", len(t.Value), bwEntryLen)
		}
		port, rate := int(t.Value[bwOffPort]), int(binary.BigEndian.Uint32(t.Value[bwOffRate:]))
		pb := byPort[port]
		if pb == nil {
			pb = &PortBandwidth{Port: port}
			byPort[port] = pb
		}
		if t.Type == wire.TLVBandwidthIngress {
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
	in, err := c.getTLVs(ctx, wire.TLVBandwidthIngress)
	if err != nil {
		return nil, err
	}
	eg, err := c.getTLVs(ctx, wire.TLVBandwidthEgress)
	if err != nil {
		return nil, err
	}
	return parseBandwidth(append(in, eg...))
}

// Layout of a bandwidth entry (TLVs 12544/12545): port (1 byte), enabled flag
// (1 byte), rate in kbps (4 bytes).
const (
	bwOffPort    = 0
	bwOffEnabled = 1
	bwOffRate    = 2
	bwEntryLen   = bwOffRate + 4
)

// MaxBandwidthKbps is the largest limit the web UI offers.
const MaxBandwidthKbps = 1000000

func bandwidthTLV(typ int16, port, kbps int) (wire.TLV, error) {
	if !validateRateKbps(kbps) {
		return wire.TLV{}, invalidf("escp: invalid rate %d kbps for port %d: use 0 (unlimited) or a multiple of %d up to %d", kbps, port, RateStepKbps, MaxBandwidthKbps)
	}
	v := make([]byte, bwEntryLen)
	v[bwOffPort] = byte(port)
	if kbps != 0 {
		v[bwOffEnabled] = 1
	}
	binary.BigEndian.PutUint32(v[bwOffRate:], uint32(kbps))
	return wire.TLV{Type: typ, Value: v}, nil
}

// SetBandwidth sets a port's ingress and egress limits in kbps (0 = unlimited,
// otherwise a multiple of 64; the same type Bandwidth reads). Like the vendor utility it sends two requests,
// ingress first, then egress. A limit on a port that carries traffic throttles
// it; callers must guard against that.
func (c *Client) SetBandwidth(ctx context.Context, limit PortBandwidth) error {
	if err := ValidatePort(limit.Port); err != nil {
		return err
	}
	in, err := bandwidthTLV(wire.TLVBandwidthIngress, limit.Port, limit.IngressKbps)
	if err != nil {
		return err
	}
	eg, err := bandwidthTLV(wire.TLVBandwidthEgress, limit.Port, limit.EgressKbps)
	if err != nil {
		return err
	}
	if err := c.requirePorts(ctx, limit.Port); err != nil {
		return err
	}
	if err := c.setTLVs(ctx, in); err != nil {
		return err
	}
	return c.setTLVs(ctx, eg)
}
