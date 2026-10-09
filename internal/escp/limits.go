package escp

import "fmt"

const (
	// MaxPort is the highest port number a 32-bit port mask can express.
	MaxPort = 32
	// RateStepKbps is the granularity of rate limits: the switch rounds to multiples of 64 kbps.
	RateStepKbps = 64
	// MaxHostnameLen is the longest device description the switch accepts.
	MaxHostnameLen = 32
	// maxByteID is the largest LAG group or VLAN id: ids are one byte on the wire.
	maxByteID = 255
	// maskLen is the size of a port mask on the wire.
	maskLen = 4
)

// validatePort checks that a port number can be addressed in a port mask.
func validatePort(port int) error {
	if port < 1 || port > MaxPort {
		return fmt.Errorf("escp: invalid port %d (valid: 1-%d)", port, MaxPort)
	}
	return nil
}

// validateRateKbps checks a rate limit: 0 is unlimited, otherwise a multiple
// of RateStepKbps up to MaxBandwidthKbps.
func validateRateKbps(kbps int) bool {
	return kbps >= 0 && kbps <= MaxBandwidthKbps && kbps%RateStepKbps == 0
}

// validateByteID checks an id (LAG group, port-based VLAN) that is one byte on the wire.
func validateByteID(kind string, id int) error {
	if id < 1 || id > maxByteID {
		return fmt.Errorf("escp: invalid %s %d (valid: 1-%d)", kind, id, maxByteID)
	}
	return nil
}
