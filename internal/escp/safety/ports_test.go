package safety

import (
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
