package escp

import "strconv"

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

// Kbps is a rate limit in kilobits per second, as the bandwidth and storm control pages take it:
// 0 is unlimited (or, for storm control, no limit set), otherwise a multiple of RateStepKbps up to
// MaxBandwidthKbps. It is the unit of the switch's own rate fields.
type Kbps int

// Validate reports whether k is a rate the switch can hold; the error matches ErrInvalid and says
// what is allowed.
func (k Kbps) Validate() error {
	if k < 0 || k > MaxBandwidthKbps || k%RateStepKbps != 0 {
		return invalidf("%d kbps is not a valid rate: use 0 (unlimited) or a multiple of %d up to %d", int(k), RateStepKbps, MaxBandwidthKbps)
	}
	return nil
}

// String is the rate with its unit, "128 kbps".
func (k Kbps) String() string { return strconv.Itoa(int(k)) + " kbps" }

// validateByteID checks an id (LAG group, port-based VLAN) that is one byte on the wire.
func validateByteID(kind string, id int) error {
	if id < 1 || id > MaxByteID {
		return invalidf("escp: invalid %s %d (valid: 1-%d)", kind, id, MaxByteID)
	}
	return nil
}
