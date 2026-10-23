package escp

import "fmt"

// Impact classes what a write can do to the switch's users, as the worst the method can do
// with any arguments. It is an upper bound for a gate or a front end deciding how much
// confirmation a write needs; it cannot say whether a particular write is harmless (a speed
// change on a free port is not, to the library, different from one on the uplink). That
// needs the ports: see Operation.Ports and the safety package.
type Impact int

// The impact classes, from harmless to able to cut the management path. The zero value is
// not a class: a writer that forgot to declare one would be refused by every gate that
// compares.
const (
	// ImpactCosmetic cannot affect traffic or the way the switch is reached (LED, device
	// description).
	ImpactCosmetic Impact = iota + 1
	// ImpactTraffic can affect the traffic of the ports it names or of the whole switch:
	// port settings, rate limits, storm control, QoS, IGMP, port mirroring and the VLAN
	// groups.
	ImpactTraffic
	// ImpactManagement can cut the management path or the uplink, or leave the switch
	// unreachable: its IP settings, the VLAN mode, PVIDs, link aggregation, loop prevention.
	ImpactManagement
)

// String returns the lower-case name of the class, or unknown(n) for a value that is none.
func (i Impact) String() string {
	switch i {
	case ImpactCosmetic:
		return "cosmetic"
	case ImpactTraffic:
		return "traffic"
	case ImpactManagement:
		return "management"
	}
	return fmt.Sprintf("unknown(%d)", int(i))
}

// Operation describes a write to a WriteGate before it is sent.
type Operation struct {
	// Name is the name of the client method that is writing ("SetPort", "SetLAG").
	Name string
	// Ports are the ports the write names, in the order the method received them: the
	// mirror's destination and sources, the members of a LAG or a VLAN, the port of a
	// port setting. Empty for a write that names none. The gate gets its own copy.
	Ports []int
	// Impact is the class of the method; see Impact.
	Impact Impact
}
