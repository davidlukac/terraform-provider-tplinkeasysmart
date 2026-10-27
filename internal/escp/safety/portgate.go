package safety

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// ErrCannotCheckPorts is what PortGate returns when it cannot establish what the ports are used
// for: the reader it was given cannot list them, or reading them failed. The write is refused,
// because a gate that cannot look must not wave a write through.
var ErrCannotCheckPorts = errors.New("safety: cannot check what the ports are used for")

// portReader is what PortGate reads through: the client has all of it.
type portReader interface {
	Ports(ctx context.Context) ([]escp.Port, error)
	LAGs(ctx context.Context) ([]escp.LAG, error)
	PortBasedVLAN(ctx context.Context) (escp.PortBasedVLAN, error)
	Dot1Q(ctx context.Context) (escp.Dot1Q, error)
}

// PortGate returns a gate that refuses a write unless every port it touches is free: it must
// exist, have no link and belong to no LAG (CheckPorts, with the same *PortUseError). It looks
// at the ports the operation names and, for a write that replaces or removes a group of ports
// (a LAG, a port-based VLAN, an 802.1Q VLAN), at the group's current members too, because
// taking the uplink out of VLAN 1 is as dangerous as naming it. A LAG's own idle members are
// tolerated when the LAG itself is edited; a link never is. Operations that touch no port
// pass without a read.
//
// It reads through the client it guards (the gate is handed the client, which has the readers),
// before every write it checks, so it costs reads and is for a front end that wants the rule
// enforced by the library instead of by its own code. A front end with an opt-in leaves it out.
// Failures to look are refused with ErrCannotCheckPorts.
func PortGate() escp.WriteGate {
	return func(ctx context.Context, sw escp.InfoReader, op escp.Operation) error {
		touched := op.Ports
		if op.Replaces == 0 && touched.IsEmpty() {
			return nil
		}
		r, ok := sw.(portReader)
		if !ok {
			return fmt.Errorf("%w: %T cannot list ports", ErrCannotCheckPorts, sw)
		}
		if op.Replaces != 0 {
			members, err := membersOf(ctx, r, op.Replaces, op.ID)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrCannotCheckPorts, err)
			}
			touched = touched.Union(members)
		}
		if touched.IsEmpty() {
			return nil
		}
		all, err := r.Ports(ctx)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrCannotCheckPorts, err)
		}
		rule := PortRule{}
		if op.Replaces == escp.GroupLAG {
			rule.TolerateLAG = escp.LAGID(op.ID) // the group number of a LAG write is the LAG id
		}
		return CheckPorts(all, rule, touched)
	}
}

// membersOf lists the ports that belong to a group now (none for a group that does not exist yet).
func membersOf(ctx context.Context, r portReader, kind escp.GroupKind, id int) (escp.PortSet, error) {
	switch kind {
	case escp.GroupLAG:
		lags, err := r.LAGs(ctx)
		if err != nil {
			return 0, err
		}
		want := escp.LAGID(id)
		for _, l := range lags {
			if l.ID == want {
				return l.Ports, nil
			}
		}
	case escp.GroupPortVLAN:
		v, err := r.PortBasedVLAN(ctx)
		if err != nil {
			return 0, err
		}
		for _, g := range v.VLANs {
			if g.ID == id {
				return g.Ports, nil
			}
		}
	case escp.GroupDot1QVLAN:
		q, err := r.Dot1Q(ctx)
		if err != nil {
			return 0, err
		}
		want := escp.VLANID(id) // the group number of a 802.1Q write is the VLAN id
		for _, v := range q.VLANs {
			if v.ID == want {
				return v.Tagged.Union(v.Untagged), nil
			}
		}
	default:
		return 0, fmt.Errorf("unknown group kind %d", int(kind))
	}
	return 0, nil
}
