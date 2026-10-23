package safety

import (
	"errors"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

func TestUseOf(t *testing.T) {
	ports := []escp.Port{
		{ID: 1, ActualSpeed: "1000Mbps-full", LAG: 1},
		{ID: 2, ActualSpeed: escp.SpeedLinkDown, LAG: 1},
		{ID: 3, ActualSpeed: "100Mbps-full"},
		{ID: 4, ActualSpeed: escp.SpeedLinkDown},
	}
	cases := []struct {
		name       string
		ports      []escp.Port
		id         int
		want       PortUse
		wantLinkUp bool
		wantInLAG  bool
	}{
		{"link up and in a LAG", ports, 1, PortUse{Exists: true, Speed: "1000Mbps-full", LAG: 1}, true, true},
		{"link down but in a LAG", ports, 2, PortUse{Exists: true, Speed: escp.SpeedLinkDown, LAG: 1}, false, true},
		{"link up, no LAG", ports, 3, PortUse{Exists: true, Speed: "100Mbps-full"}, true, false},
		{"unused", ports, 4, PortUse{Exists: true, Speed: escp.SpeedLinkDown}, false, false},
		{"not listed", ports, 9, PortUse{}, false, false},
		{"empty reading", nil, 1, PortUse{}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UseOf(tc.ports, tc.id)
			if got != tc.want {
				t.Fatalf("UseOf = %+v, want %+v", got, tc.want)
			}
			if got.LinkUp() != tc.wantLinkUp || got.InLAG() != tc.wantInLAG {
				t.Fatalf("LinkUp, InLAG = %v, %v; want %v, %v", got.LinkUp(), got.InLAG(), tc.wantLinkUp, tc.wantInLAG)
			}
		})
	}
}

// A port the switch does not list has no speed, which must not read as "link up".
func TestMissingPortIsNotLinkUp(t *testing.T) {
	if (PortUse{}).LinkUp() {
		t.Fatal("a port that does not exist reports a link")
	}
}

// A port listed twice counts as used if any entry says so: the guard fails closed.
func TestUseOfFailsClosedOnDuplicateEntries(t *testing.T) {
	cases := []struct {
		name  string
		ports []escp.Port
		want  PortUse
	}{
		{"LAG on the second entry", []escp.Port{{ID: 5, ActualSpeed: escp.SpeedLinkDown}, {ID: 5, ActualSpeed: escp.SpeedLinkDown, LAG: 2}}, PortUse{Exists: true, Speed: escp.SpeedLinkDown, LAG: 2}},
		{"link on the second entry", []escp.Port{{ID: 5, ActualSpeed: escp.SpeedLinkDown}, {ID: 5, ActualSpeed: "100Mbps-full"}}, PortUse{Exists: true, Speed: "100Mbps-full"}},
		{"first entry wins when it already says used", []escp.Port{{ID: 5, ActualSpeed: "1000Mbps-full", LAG: 1}, {ID: 5, ActualSpeed: "100Mbps-full", LAG: 2}}, PortUse{Exists: true, Speed: "1000Mbps-full", LAG: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := UseOf(tc.ports, 5); got != tc.want {
				t.Fatalf("UseOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func checkedPorts() []escp.Port {
	return []escp.Port{
		{ID: 1, ActualSpeed: "1000Mbps-full", LAG: 1}, // linked and in LAG 1
		{ID: 2, ActualSpeed: escp.SpeedLinkDown, LAG: 1},
		{ID: 3, ActualSpeed: "100Mbps-full"},
		{ID: 4, ActualSpeed: escp.SpeedLinkDown},
		{ID: 5, ActualSpeed: escp.SpeedLinkDown},
		{ID: 6, ActualSpeed: escp.SpeedLinkDown, LAG: 2},
	}
}

func TestCheckPorts(t *testing.T) {
	cases := []struct {
		name  string
		rule  PortRule
		ports []int
		want  *PortUseError // nil: every port is free
	}{
		{"free ports", PortRule{}, []int{4, 5}, nil},
		{"no ports", PortRule{}, nil, nil},
		{"a port that does not exist", PortRule{}, []int{9}, &PortUseError{Port: 9, Reason: PortMissing}},
		{"a port with a link", PortRule{}, []int{3}, &PortUseError{Port: 3, Reason: PortLinkUp, Speed: "100Mbps-full"}},
		{"a LAG member without a link", PortRule{}, []int{2}, &PortUseError{Port: 2, Reason: PortInLAG, LAG: 1}},
		{"the link is named before the LAG", PortRule{}, []int{1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}},
		{"missing is named before anything else about the same port", PortRule{}, []int{9, 3}, &PortUseError{Port: 9, Reason: PortMissing}},
		{"the first offender in the order given", PortRule{}, []int{4, 3, 2}, &PortUseError{Port: 3, Reason: PortLinkUp, Speed: "100Mbps-full"}},
		{"the same port twice", PortRule{}, []int{4, 4, 2, 2}, &PortUseError{Port: 2, Reason: PortInLAG, LAG: 1}},
		{"members of the tolerated LAG", PortRule{TolerateLAG: 1}, []int{2}, nil},
		{"another LAG is not tolerated", PortRule{TolerateLAG: 1}, []int{6}, &PortUseError{Port: 6, Reason: PortInLAG, LAG: 2}},
		{"a link is never tolerated, not even in the tolerated LAG", PortRule{TolerateLAG: 1}, []int{1}, &PortUseError{Port: 1, Reason: PortLinkUp, Speed: "1000Mbps-full", LAG: 1}},
		{"no group tolerated", PortRule{TolerateLAG: 0}, []int{2}, &PortUseError{Port: 2, Reason: PortInLAG, LAG: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPorts(checkedPorts(), tc.rule, tc.ports...)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("want no refusal, got %v", err)
				}
				return
			}
			var got *PortUseError
			if !errors.As(err, &got) {
				t.Fatalf("want a *PortUseError, got %v", err)
			}
			if *got != *tc.want {
				t.Fatalf("got %+v, want %+v", *got, *tc.want)
			}
		})
	}
}

// The neutral message names the port and the fact, and nothing about how to proceed: that is each
// front end's to word.
func TestPortUseErrorMessages(t *testing.T) {
	cases := []struct {
		err  *PortUseError
		want string
	}{
		{&PortUseError{Port: 9, Reason: PortMissing}, "port 9 does not exist on this switch"},
		{&PortUseError{Port: 3, Reason: PortLinkUp, Speed: "100Mbps-full"}, "port 3 has a link up (100Mbps-full)"},
		{&PortUseError{Port: 2, Reason: PortInLAG, LAG: 1}, "port 2 is a member of LAG 1"},
	}
	for _, tc := range cases {
		if got := tc.err.Error(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

// Nothing in the reading is changed by a check.
func TestCheckPortsLeavesTheReadingAlone(t *testing.T) {
	all := checkedPorts()
	before := append([]escp.Port(nil), all...)
	_ = CheckPorts(all, PortRule{}, 1, 2, 3)
	if !reflect.DeepEqual(all, before) {
		t.Fatal("the reading was modified")
	}
}
