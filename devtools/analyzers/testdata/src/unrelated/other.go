// Package unrelated is not one of the packages the check is set to look at (the default is the
// escp-go root package), so nothing here is reported.
package unrelated

// Port is a value with its own rule.
type Port int

// Validate refuses a port outside 1-32.
func (p Port) Validate() error { return nil }

// Take does not validate.
func Take(p Port) int { return int(p) }
