package safety

import "github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"

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
	Speed string
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
func UseOf(ports []escp.Port, id int) PortUse {
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
