package regression

import (
	"errors"
	"fmt"
	"slices"

	"github.com/davidlukac/escp-go/model"
)

// What the scenarios ask of a target, stated once. LoadTarget checks syntax and the rules that keep
// a run safe (overlaps, the device-port subset, the spare address); this file holds what the
// scenarios need beyond that to run at all, and the conditions under which they skip. A scenario
// takes its ports and its skip conditions from here, and TestExampleEnvFile holds the sample file
// (.test.env.example) to the same code, so a new scenario with a new need extends this file and
// nothing else.

const (
	// MinTestPorts is how many test ports the scenarios address: TestPorts[0] to TestPorts[3].
	MinTestPorts = 4
	// MinProtectedPorts is how many protected ports the guard-refusal scenario names: the management
	// port, listed first, and three device ports.
	MinProtectedPorts = 4
	// ScenarioLAGGroup is the LAG group the LAG scenarios write. It takes the ports of
	// model.LAGID.Range only, which is why a scenario's ports must be chosen with it in mind.
	ScenarioLAGGroup model.LAGID = 1
	// lagExcludedPort is never part of the pair the disruptive LAG scenario builds: it is the first
	// port of group 1, where the management port conventionally sits.
	lagExcludedPort model.PortID = 1
)

// The needs a target can fail, each a sentinel so that a test tells them apart.
var (
	errTooFewTestPorts      = errors.New("too few test ports")
	errDuplicateTestPorts   = errors.New("a test port is listed twice")
	errTestPortsLAG         = errors.New("the test ports a LAG scenario uses do not fit its group")
	errTooFewProtectedPorts = errors.New("too few protected ports")
	errUplinkLAG            = errors.New("the ports of the LAG-with-the-uplink refusal do not fit its group")
	errNotDevicePort        = errors.New("not a device port (a protected port other than the first)")
)

// DevicePorts are the ports that carry a device: the protected ports other than the management
// port, which is listed first.
func (t Target) DevicePorts() []model.PortID {
	return t.ProtectedPorts[min(1, len(t.ProtectedPorts)):]
}

// LAGTestPorts are the two test ports the LAG scenarios put in group ScenarioLAGGroup.
func (t Target) LAGTestPorts() (a, b model.PortID) { return t.TestPorts[1], t.TestPorts[2] }

// UplinkLAGPorts are the ports of the guard-refusal case "a LAG with the uplink": the management
// port (the first protected port) and the first test port, in group ScenarioLAGGroup.
func (t Target) UplinkLAGPorts() (uplink, test model.PortID) {
	return t.ProtectedPorts[0], t.TestPorts[0]
}

// HasGuardPorts reports whether the target names the protected ports the guard-refusal scenario
// needs; the scenario skips otherwise.
func (t Target) HasGuardPorts() bool { return len(t.ProtectedPorts) >= MinProtectedPorts }

// CanMoveManagement reports whether the run opted in to the management-move scenario and gave it a
// spare address; the scenarios skip otherwise.
func (t Target) CanMoveManagement() bool { return t.MoveIP != "" }

// CanUseDHCP reports whether the run opted in to the DHCP scenario; it skips otherwise.
func (t Target) CanUseDHCP() bool { return t.DHCP }

// CanDisruptDevicePort reports whether the run named the device port the allow_disruptive scenario
// may write to; on hardware it skips otherwise.
func (t Target) CanDisruptDevicePort() bool { return t.DisruptPort != 0 }

// CanDisrupt reports whether the run named the device ports the disruptive scenarios may change;
// they skip otherwise.
func (t Target) CanDisrupt() bool { return len(t.DisruptivePorts) > 0 }

// LAGPair is two device ports that fit one LAG group.
type LAGPair struct {
	A, B  model.PortID
	Group model.LAGID
}

// DisruptiveLAGPairs lists the pairs of disruptive ports that fit one LAG group: the first port of a
// pair is the lower one, and the pairs come in the order of DisruptivePorts. Port 1 never starts a
// pair. The disruptive LAG scenario picks among them by link state, which only the switch knows, and
// skips when none is left, so a target with none never runs it.
func (t Target) DisruptiveLAGPairs() []LAGPair {
	var out []LAGPair
	for _, a := range t.DisruptivePorts {
		for _, b := range t.DisruptivePorts {
			// a >= b only drops the mirror image of a pair; a > b would let a == b through, which lagFits
			// refuses (a group takes two ports at least), so no input tells the two apart
			if a >= b || a == lagExcludedPort {
				continue
			}
			for g := model.LAGID(1); g <= model.LAGGroupCount; g++ {
				if lagFits(g, a, b) == nil {
					out = append(out, LAGPair{A: a, B: b, Group: g})
					break // the groups own disjoint ports, so a pair fits one; this only stops the search early
				}
			}
		}
	}
	return out
}

// ScenarioNeeds returns nil when the target has what the scenarios need to run, and otherwise every
// need it fails, joined; each matches one of the sentinels above with errors.Is. The opt-in
// scenarios are not needs, a run may leave them out; what they ask of the ports it did name is.
func (t Target) ScenarioNeeds() error {
	var errs []error
	enoughTest, enoughProtected := len(t.TestPorts) >= MinTestPorts, len(t.ProtectedPorts) >= MinProtectedPorts
	if enoughTest {
		errs = append(errs, t.testPortNeeds()...)
	} else {
		errs = append(errs, fmt.Errorf("%w: %v, the scenarios address %d", errTooFewTestPorts, t.TestPorts, MinTestPorts))
	}
	if !enoughProtected {
		errs = append(errs, fmt.Errorf("%w: %v, the guard scenario names %d", errTooFewProtectedPorts, t.ProtectedPorts, MinProtectedPorts))
	}
	if enoughTest && enoughProtected {
		uplink, test := t.UplinkLAGPorts()
		if err := lagFits(ScenarioLAGGroup, uplink, test); err != nil {
			errs = append(errs, fmt.Errorf("%w: %d and %d: %w", errUplinkLAG, uplink, test, err))
		}
	}
	return errors.Join(append(errs, t.devicePortNeeds()...)...)
}

// testPortNeeds is what the scenarios ask of four or more test ports: they are different ports
// (a scenario pairs two of them, or mirrors one to another), and the pair of the LAG scenarios
// fits its group.
func (t Target) testPortNeeds() []error {
	var errs []error
	for i, p := range t.TestPorts {
		if slices.Contains(t.TestPorts[:i], p) {
			errs = append(errs, fmt.Errorf("%w: port %d", errDuplicateTestPorts, p))
		}
	}
	a, b := t.LAGTestPorts()
	if err := lagFits(ScenarioLAGGroup, a, b); err != nil {
		errs = append(errs, fmt.Errorf("%w: %d and %d: %w", errTestPortsLAG, a, b, err))
	}
	return errs
}

// devicePortNeeds is what the opt-in scenarios ask of the ports the run named: each is a device
// port.
func (t Target) devicePortNeeds() []error {
	var errs []error
	named := t.DisruptivePorts
	if t.DisruptPort != 0 {
		named = append(slices.Clone(named), t.DisruptPort)
	}
	for _, p := range named {
		if !slices.Contains(t.DevicePorts(), p) {
			errs = append(errs, fmt.Errorf("%w: port %d, the device ports are %v", errNotDevicePort, p, t.DevicePorts()))
		}
	}
	return errs
}

// lagFits checks ports against the rules of one LAG group (the library's own: the group's range and
// its size, at least two ports).
func lagFits(group model.LAGID, ports ...model.PortID) error {
	set, err := model.NewPortSet(ports...)
	if err != nil {
		return err
	}
	return model.LAG{ID: group, Ports: set}.Validate()
}
