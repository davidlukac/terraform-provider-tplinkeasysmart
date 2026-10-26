package safety

import (
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// PortUse is what a write to one port could disturb. It states facts only: how a
// caller orders its checks and what it says about them differs per front end (a LAG
// edit tolerates the ports of its own group, a VLAN edit does not), so the verdicts
// stay with the caller.
type PortUse struct {
	// Exists reports whether the switch has the port at all. When false, the other
	// fields are zero.
	Exists bool
	// Speed is the negotiated speed as the client reports it (escp.SpeedLinkDown
	// without a link).
	Speed escp.Speed
	// LAG is the link aggregation group the port belongs to, 0 for none.
	LAG int
}

// LinkUp reports whether the port carries a link, and so may carry traffic or the
// management path.
func (u PortUse) LinkUp() bool { return u.Exists && u.Speed != escp.SpeedLinkDown }

// InLAG reports whether the port is a member of a link aggregation group.
func (u PortUse) InLAG() bool { return u.LAG != 0 }

// UseOf looks the port up in a Ports reading. A port the switch does not list is
// reported as not existing. A port listed more than once (a real switch never does)
// counts as used if any of its entries says so, so the answer fails closed.
func UseOf(ports []escp.Port, id escp.PortID) PortUse {
	var use PortUse
	for _, p := range ports {
		if p.ID != id {
			continue
		}
		if !use.Exists {
			use = PortUse{Exists: true, Speed: p.ActualSpeed, LAG: p.LAG}
			continue
		}
		if use.LAG == 0 {
			use.LAG = p.LAG
		}
		if use.Speed == escp.SpeedLinkDown {
			use.Speed = p.ActualSpeed
		}
	}
	return use
}

// PortReason says why a port check refused a port.
type PortReason int

// The reasons CheckPorts refuses. They are listed in the order the checks run.
const (
	// PortMissing: the switch has no such port.
	PortMissing PortReason = iota + 1
	// PortLinkUp: the port has a link, so it may carry traffic or the management path.
	PortLinkUp
	// PortInLAG: the port belongs to a link aggregation group.
	PortInLAG
)

// PortUseError is the refusal of CheckPorts: the first port that a write should not touch
// without an explicit decision, and why. Callers branch on Reason and word the message in their
// own vocabulary, as with FirmwareError; Error is a neutral description.
type PortUseError struct {
	Port   escp.PortID
	Reason PortReason
	Speed  escp.Speed // the negotiated speed (PortLinkUp)
	LAG    int        // the group (PortInLAG)
}

// Error names the port and why it may not be written: it does not exist, it has a link, or it is in a
// LAG.
func (e *PortUseError) Error() string {
	switch e.Reason {
	case PortMissing:
		return fmt.Sprintf("port %d does not exist on this switch", e.Port)
	case PortLinkUp:
		return fmt.Sprintf("port %d has a link up (%s)", e.Port, e.Speed)
	default:
		return fmt.Sprintf("port %d is a member of LAG %d", e.Port, e.LAG)
	}
}

// PortRule says which uses CheckPorts tolerates. The zero value tolerates none.
type PortRule struct {
	// TolerateLAG is a group whose members are fine, for an edit of that very group; zero for
	// none. A link is never tolerated.
	TolerateLAG int
}

// CheckPorts returns a *PortUseError for the lowest-numbered of ports that does not exist, has a link up, or belongs to a LAG the rule does not tolerate, in
// that order for each port. It returns nil when every port is free. all is a reading of the
// ports (Client.Ports); a write to a port that fails the check can cut the management path or
// the uplink, and the switch has no console to recover from that.
func CheckPorts(all []escp.Port, rule PortRule, ports escp.PortSet) error {
	for _, p := range ports.Ports() {
		use := UseOf(all, p)
		switch {
		case !use.Exists:
			return &PortUseError{Port: p, Reason: PortMissing}
		case use.LinkUp():
			return &PortUseError{Port: p, Reason: PortLinkUp, Speed: use.Speed, LAG: use.LAG}
		case use.InLAG() && use.LAG != rule.TolerateLAG:
			return &PortUseError{Port: p, Reason: PortInLAG, LAG: use.LAG}
		}
	}
	return nil
}
