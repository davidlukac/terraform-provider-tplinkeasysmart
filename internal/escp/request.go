package escp

import (
	"context"
	"fmt"
	"net"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// setTLVs sends a SET carrying the credentials plus the given settings. It is
// the only place where the client changes the switch, so the write gate sits
// here: no setting can be added without passing it. (The two other SETs are the
// session key exchange and ConfigBackup, which only reads; a test pins every
// place that sends an OpSet.) The gate runs before the lock is
// taken because it reads through the client. op describes the write to the gate
// (the gate gets a copy of its ports).
func (c *Client) setTLVs(ctx context.Context, op Operation, tlvs ...wire.TLV) error {
	_, err := c.setTLVsSent(ctx, op, tlvs...)
	return err
}

// setTLVsSent is setTLVs that also reports whether the gate let the write go, so that the
// request may have reached the switch: false means nothing was sent for certain.
func (c *Client) setTLVsSent(ctx context.Context, op Operation, tlvs ...wire.TLV) (sent bool, err error) {
	if gate := c.cfg.WriteGate; gate != nil {
		if err := gate(ctx, c, op); err != nil {
			return false, err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return true, c.withSession(ctx, func() error {
		req := append([]wire.TLV{
			{Type: wire.TLVUsername, Value: append([]byte(c.cfg.Username), 0)},
			{Type: wire.TLVPassword, Value: append([]byte(c.cfg.Password), 0)},
		}, tlvs...)
		r, err := c.exchange(ctx, wire.OpSet, req, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		if r.err != 0 {
			return &RejectedError{Code: r.err}
		}
		return nil
	})
}

// getTLVs reads one or more setting types with an authenticated session and
// returns every TLV of the reply. It never writes.
func (c *Client) getTLVs(ctx context.Context, types ...wire.TLVType) ([]wire.TLV, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []wire.TLV
	err := c.withSession(ctx, func() error {
		req := make([]wire.TLV, len(types))
		for i, t := range types {
			req[i] = wire.TLV{Type: t}
		}
		r, err := c.exchange(ctx, wire.OpGet, req, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		if r.err != 0 {
			return fmt.Errorf("escp: switch answered error code %d for TLV types %v", r.err, types)
		}
		out = r.tlvs
		return nil
	})
	return out, err
}

// requirePorts refuses ports the switch does not have. A PortSet only holds ports that fit
// the 32-bit port mask; the switch may have far fewer (8 on a TL-SG108E), and
// what it does with a write for a port beyond them is unknown. The port count is read from
// the switch once and kept, so the first write that names a port costs one extra read; if
// that read fails, so does the write, with that error. It reads only, and so is not gated.
func (c *Client) requirePorts(ctx context.Context, ports PortSet) error {
	if ports.IsEmpty() {
		return nil
	}
	have, err := c.portCount(ctx)
	if err != nil {
		return err
	}
	last := PortID(have) // the ports are numbered 1 to the count
	for _, p := range ports.Ports() {
		if p > last {
			return invalidf("escp: port %d does not exist: this switch has %d ports", p, have)
		}
	}
	return nil
}

// portCount returns the switch's number of ports, reading it on first use.
func (c *Client) portCount(ctx context.Context) (int, error) {
	c.mu.Lock()
	known := c.switchPorts
	c.mu.Unlock()
	if known > 0 {
		return known, nil
	}
	tlvs, err := c.getTLVs(ctx, wire.TLVPortCount)
	if err != nil {
		return 0, err
	}
	for _, t := range tlvs {
		if t.Type == wire.TLVPortCount && len(t.Value) == 1 && t.Value[0] > 0 {
			c.mu.Lock()
			c.switchPorts = int(t.Value[0])
			c.mu.Unlock()
			return int(t.Value[0]), nil
		}
	}
	return 0, fmt.Errorf("escp: the switch did not report its number of ports")
}
