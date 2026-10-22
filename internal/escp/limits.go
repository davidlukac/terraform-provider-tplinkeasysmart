package escp

import (
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

const (
	// MaxPort is the highest port number a 32-bit port mask can express.
	MaxPort = 32
	// RateStepKbps is the granularity of rate limits: the switch rounds to multiples of 64 kbps.
	RateStepKbps = 64
	// MaxHostnameLen is the longest device description the switch accepts.
	MaxHostnameLen = 32
	// MaxByteID is the largest LAG group or VLAN id: ids are one byte on the wire.
	MaxByteID = 255
	// maskLen is the size of a port mask on the wire.
	maskLen = 4
)

// ValidatePort checks that a port number can be addressed in a port mask. It cannot know how
// many ports the switch has: the writers that take a port also refuse ports beyond the switch's own
// count (see requirePorts).
func ValidatePort(port int) error {
	if port < 1 || port > MaxPort {
		return invalidf("escp: invalid port %d (valid: 1-%d)", port, MaxPort)
	}
	return nil
}

// requirePorts refuses ports the switch does not have. ValidatePort has already shown that
// each port fits the 32-bit port mask; the switch may have far fewer (8 on a TL-SG108E), and
// what it does with a write for a port beyond them is unknown. The port count is read from
// the switch once and kept, so the first write that names a port costs one extra read; if
// that read fails, so does the write, with that error. It reads only, and so is not gated.
func (c *Client) requirePorts(ctx context.Context, ports ...int) error {
	if len(ports) == 0 {
		return nil
	}
	have, err := c.portCount(ctx)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if p > have {
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

// validateRateKbps reports whether kbps is a valid rate limit: 0 is
// unlimited, otherwise a multiple of RateStepKbps up to MaxBandwidthKbps.
func validateRateKbps(kbps int) bool {
	return kbps >= 0 && kbps <= MaxBandwidthKbps && kbps%RateStepKbps == 0
}

// ValidateRateKbps checks a rate limit (see validateRateKbps) and explains what is allowed.
func ValidateRateKbps(kbps int) error {
	if !validateRateKbps(kbps) {
		return invalidf("%d kbps is not a valid rate: use 0 (unlimited) or a multiple of %d up to %d", kbps, RateStepKbps, MaxBandwidthKbps)
	}
	return nil
}

// validateByteID checks an id (LAG group, port-based VLAN) that is one byte on the wire.
func validateByteID(kind string, id int) error {
	if id < 1 || id > MaxByteID {
		return invalidf("escp: invalid %s %d (valid: 1-%d)", kind, id, MaxByteID)
	}
	return nil
}
