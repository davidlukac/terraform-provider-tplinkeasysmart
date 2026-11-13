// Package validatecallnames is looked at with the name of the validating method set to Check: only the
// type with a Check method is a type with a rule, and Validate is a method like any other.
package validatecallnames

// Port has a Validate method, which this run does not take for a rule.
type Port int

// Validate is not a validating name in this run.
func (p Port) Validate() error { return nil }

// Thing has the validating method of this run.
type Thing int

// Check is the rule of a Thing.
func (t Thing) Check() error { return nil }

// TakePort is not reported: Port has no Check method.
func TakePort(p Port) int { return int(p) }

// TakeThing is reported: the rule is Check and it is not called.
func TakeThing(t Thing) int { return int(t) } // want `TakeThing takes t \(Thing has a Check method\) and never validates it`

// TakeThingChecked is silent: it calls Check.
func TakeThingChecked(t Thing) error { return t.Check() }
