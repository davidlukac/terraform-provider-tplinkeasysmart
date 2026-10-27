package safety

import (
	"context"
	"errors"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// switchView is a switch as the gate can read it, counting the reads.
type switchView struct {
	ports    []escp.Port
	lags     []escp.LAG
	pvlans   escp.PortBasedVLAN
	dot1q    escp.Dot1Q
	failWith error  // the read named in failOn fails with it
	failOn   string // "Ports", "LAGs", "PortBasedVLAN" or "Dot1Q"
	reads    int
}

func (s *switchView) Info(context.Context) (escp.Info, error) { return escp.Info{}, nil }

// read counts a read and fails it when it is the one the test broke.
func (s *switchView) read(name string) error {
	s.reads++
	if s.failOn == name {
		return s.failWith
	}
	return nil
}

func (s *switchView) Ports(context.Context) ([]escp.Port, error) { return s.ports, s.read("Ports") }
func (s *switchView) LAGs(context.Context) ([]escp.LAG, error)   { return s.lags, s.read("LAGs") }
func (s *switchView) PortBasedVLAN(context.Context) (escp.PortBasedVLAN, error) {
	return s.pvlans, s.read("PortBasedVLAN")
}
func (s *switchView) Dot1Q(context.Context) (escp.Dot1Q, error) { return s.dot1q, s.read("Dot1Q") }

// view: port 1 is the uplink (linked, LAG 1), 2 is its idle partner, 3 and 4 are free, 5 and 7 are idle members of LAG 2.
func view() *switchView {
	return &switchView{
		ports: []escp.Port{
			{ID: 1, ActualSpeed: "1000Mbps-full", LAG: 1},
			{ID: 2, ActualSpeed: escp.SpeedLinkDown, LAG: 1},
			{ID: 3, ActualSpeed: escp.SpeedLinkDown},
			{ID: 4, ActualSpeed: escp.SpeedLinkDown},
			{ID: 5, ActualSpeed: escp.SpeedLinkDown, LAG: 2},
			{ID: 6, ActualSpeed: "100Mbps-full"},
			{ID: 7, ActualSpeed: escp.SpeedLinkDown, LAG: 2},
		},
		lags:   []escp.LAG{{ID: 1, Ports: escp.MustPortSet(1, 2)}, {ID: 2, Ports: escp.MustPortSet(5, 7)}},
		pvlans: escp.PortBasedVLAN{VLANs: []escp.PortVLAN{{ID: 1, Ports: escp.MustPortSet(1, 3, 4)}, {ID: 2, Ports: escp.MustPortSet(3)}}},
		dot1q: escp.Dot1Q{VLANs: []escp.VLAN8021Q{
			{ID: 1, Untagged: escp.MustPortSet(1, 2, 3, 4)},
			{ID: 20, Tagged: escp.MustPortSet(6), Untagged: escp.MustPortSet(3)},
		}},
	}
}

func TestPortGate(t *testing.T) {
	cases := []struct {
		name  string
		op    escp.Operation
		want  *PortUseError // nil: allowed
		reads int           // how many reads the check is allowed to cost
	}{
		{"a write that names no port passes without a read", escp.Operation{Name: "SetLED", Impact: escp.ImpactCosmetic}, nil, 0},
		{"free ports pass", escp.Operation{Name: "SetPort", Ports: escp.MustPortSet(3, 4)}, nil, 1},
		{"a linked port is refused", escp.Operation{Name: "SetPort", Ports: escp.MustPortSet(6)}, &PortUseError{Port: 6, Reason: PortLinkUp, Speed: "100Mbps-full"}, 1},
		{"the uplink is refused by its link", escp.Operation{Name: "SetPort", Ports: escp.MustPortSet(1)}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}, 1},
		{"a port the switch does not have", escp.Operation{Name: "SetPort", Ports: escp.MustPortSet(9)}, &PortUseError{Port: 9, Reason: PortMissing}, 1},
		{"an idle LAG member is refused when not editing its LAG", escp.Operation{Name: "SetPort", Ports: escp.MustPortSet(2)}, &PortUseError{Port: 2, Reason: PortInLAG, LAG: 1}, 1},

		{"a new LAG over free ports", escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(3, 4), Replaces: escp.GroupLAG, ID: 3}, nil, 2},
		{"a LAG edit tolerates the group's own idle member", escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(5, 3), Replaces: escp.GroupLAG, ID: 2}, nil, 2},
		{"removing a LAG whose member carries the uplink is refused", escp.Operation{Name: "SetLAG", Replaces: escp.GroupLAG, ID: 1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}, 2},
		{"replacing a LAG's members is refused for the member it drops", escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(3, 4), Replaces: escp.GroupLAG, ID: 1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}, 2},
		{"a LAG edit does not tolerate another group's member", escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(2, 3), Replaces: escp.GroupLAG, ID: 2}, &PortUseError{Port: 2, Reason: PortInLAG, LAG: 1}, 2},

		{"a port-based group over free ports", escp.Operation{Name: "SetPortBasedVLAN", Ports: escp.MustPortSet(3), Replaces: escp.GroupPortVLAN, ID: 2}, nil, 2},
		{"removing the group that holds the uplink is refused", escp.Operation{Name: "SetPortBasedVLAN", Replaces: escp.GroupPortVLAN, ID: 1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}, 2},
		{"removing a group that has no members touches nothing", escp.Operation{Name: "SetLAG", Replaces: escp.GroupLAG, ID: 3}, nil, 1},
		{"a group that does not exist yet has no old members", escp.Operation{Name: "SetPortBasedVLAN", Ports: escp.MustPortSet(4), Replaces: escp.GroupPortVLAN, ID: 7}, nil, 2},

		{"an 802.1Q VLAN without a linked member", escp.Operation{Name: "SetVLAN8021Q", Ports: escp.MustPortSet(4), Replaces: escp.GroupDot1QVLAN, ID: 20}, &PortUseError{Port: 6, Reason: PortLinkUp, Speed: "100Mbps-full"}, 2},
		{"rewriting VLAN 1 so that it drops the uplink is refused", escp.Operation{Name: "SetVLAN8021Q", Ports: escp.MustPortSet(3, 4), Replaces: escp.GroupDot1QVLAN, ID: 1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}, 2},
		{"deleting VLAN 1 names no port and is still refused", escp.Operation{Name: "DeleteVLAN8021Q", Replaces: escp.GroupDot1QVLAN, ID: 1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sw := view()
			err := PortGate()(context.Background(), sw, tc.op)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want it allowed, got %v", err)
				}
			} else {
				var got *PortUseError
				if !errors.As(err, &got) || *got != *tc.want {
					t.Fatalf("got %v, want %+v", err, *tc.want)
				}
			}
			if sw.reads > tc.reads {
				t.Errorf("the check read %d times, expected at most %d", sw.reads, tc.reads)
			}
		})
	}
}

// A gate that cannot look must refuse, whatever the reason.
func TestPortGateRefusesWhenItCannotLook(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("no reply")
	named := escp.Operation{Name: "SetPort", Ports: escp.MustPortSet(3)}
	t.Run("the reader cannot list ports", func(t *testing.T) {
		err := PortGate()(ctx, plainReader{}, named)
		if !errors.Is(err, ErrCannotCheckPorts) {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("an operation that names nothing and replaces nothing needs no reader", func(t *testing.T) {
		if err := PortGate()(ctx, plainReader{}, escp.Operation{Name: "SetLED"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		reader string
		op     escp.Operation
	}{
		{"Ports", named},
		{"Ports", escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(3, 4), Replaces: escp.GroupLAG, ID: 1}}, // the groups read fine, the ports do not
		{"LAGs", escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(3, 4), Replaces: escp.GroupLAG, ID: 1}},
		{"PortBasedVLAN", escp.Operation{Name: "SetPortBasedVLAN", Ports: escp.MustPortSet(3), Replaces: escp.GroupPortVLAN, ID: 2}},
		{"Dot1Q", escp.Operation{Name: "SetVLAN8021Q", Ports: escp.MustPortSet(3), Replaces: escp.GroupDot1QVLAN, ID: 20}},
	} {
		op := tc.op
		t.Run("reading "+tc.reader+" fails for "+op.Name, func(t *testing.T) {
			sw := view()
			sw.failWith, sw.failOn = boom, tc.reader
			err := PortGate()(ctx, sw, op)
			if !errors.Is(err, ErrCannotCheckPorts) || !errors.Is(err, boom) {
				t.Fatalf("want ErrCannotCheckPorts wrapping the cause, got %v", err)
			}
		})
	}
	t.Run("a group kind it does not know", func(t *testing.T) {
		err := PortGate()(ctx, view(), escp.Operation{Name: "SetX", Replaces: escp.GroupKind(99), ID: 1})
		if !errors.Is(err, ErrCannotCheckPorts) {
			t.Fatalf("got %v", err)
		}
	})
}

// plainReader is an InfoReader that cannot list ports.
type plainReader struct{}

func (plainReader) Info(context.Context) (escp.Info, error) { return escp.Info{}, nil }

func TestPortGateUsesTheNamedPortsEvenWhenTheGroupIsEmpty(t *testing.T) {
	sw := view()
	sw.lags = nil // no LAG exists yet
	err := PortGate()(context.Background(), sw, escp.Operation{Name: "SetLAG", Ports: escp.MustPortSet(6, 3), Replaces: escp.GroupLAG, ID: 2})
	var got *PortUseError
	if !errors.As(err, &got) || got.Port != 6 {
		t.Fatalf("got %v, want port 6 refused", err)
	}
}
