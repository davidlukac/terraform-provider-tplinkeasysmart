package escp

import "fmt"

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

// ValidatePort checks that a port number can be addressed in a port mask.
func ValidatePort(port int) error {
	if port < 1 || port > MaxPort {
		return fmt.Errorf("escp: invalid port %d (valid: 1-%d)", port, MaxPort)
	}
	return nil
}

// validateRateKbps reports whether kbps is a valid rate limit: 0 is
// unlimited, otherwise a multiple of RateStepKbps up to MaxBandwidthKbps.
func validateRateKbps(kbps int) bool {
	return kbps >= 0 && kbps <= MaxBandwidthKbps && kbps%RateStepKbps == 0
}

// ValidateRateKbps checks a rate limit (see validateRateKbps) and explains what is allowed.
func ValidateRateKbps(kbps int) error {
	if !validateRateKbps(kbps) {
		return fmt.Errorf("%d kbps is not a valid rate: use 0 (unlimited) or a multiple of %d up to %d", kbps, RateStepKbps, MaxBandwidthKbps)
	}
	return nil
}

// validateByteID checks an id (LAG group, port-based VLAN) that is one byte on the wire.
func validateByteID(kind string, id int) error {
	if id < 1 || id > MaxByteID {
		return fmt.Errorf("escp: invalid %s %d (valid: 1-%d)", kind, id, MaxByteID)
	}
	return nil
}
