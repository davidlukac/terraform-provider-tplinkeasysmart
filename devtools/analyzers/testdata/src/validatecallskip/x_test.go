package validatecallskip

// Port is a value with its own rule.
type Port int

// Validate refuses a port outside 1-32.
func (p Port) Validate() error { return nil }

// TakeInATest is not reported: test files are not checked unless the flag asks.
func TakeInATest(p Port) Port { return p }
