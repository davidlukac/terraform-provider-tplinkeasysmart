package escp

import (
	"context"
	"fmt"
	"net"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// LED reports whether the switch LEDs are on.
func (c *Client) LED(ctx context.Context) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var on bool
	err := c.withSession(ctx, func() error {
		r, err := c.exchange(ctx, wire.OpGet, []wire.TLV{{Type: wire.TLVLED, Value: nil}}, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		v, ok := r.tlv(wire.TLVLED)
		if !ok || len(v) != 1 {
			return fmt.Errorf("escp: unexpected LED reply (error code %d, %d TLVs)", r.err, len(r.tlvs))
		}
		on = v[0] == 1
		return nil
	})
	return on, err
}

// SetLED switches the LEDs on or off. It is cosmetic: it does not touch traffic or
// configuration, and it is the one write that is safe to try on any switch.
func (c *Client) SetLED(ctx context.Context, on bool) error {
	return c.setTLVs(ctx, Operation{Name: "SetLED", Impact: ImpactCosmetic}, wire.TLV{Type: wire.TLVLED, Value: []byte{boolByte(on)}})
}
