package validatecalltests

// Port is a value with its own rule.
type Port int

// Validate refuses a port outside 1-32.
func (p Port) Validate() error { return nil }

// TakeInATest is checked only when the flag asks for it.
func TakeInATest(p Port) Port { return p } // want `TakeInATest takes p \(Port has a Validate method\) and never validates it`
