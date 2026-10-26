package escp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Parsers receive replies that may carry TLV types they do not own (a GET of
// several types, or a switch answering more than asked). They must ignore them
// and still decode what is theirs.
func TestParsersIgnoreForeignTLVs(t *testing.T) {
	foreign := wire.TLV{Type: 9999, Value: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}
	tests := []struct {
		name string
		got  func() (any, error)
		want any
	}{
		{"ports", func() (any, error) { return parsePorts([]wire.TLV{foreign, th(wire.TLVPorts, "03010001000000")}) },
			[]Port{{ID: 3, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"}}},
		{"port statistics", func() (any, error) { return parsePortStats([]wire.TLV{foreign}) }, []PortStats(nil)},
		{"LAGs", func() (any, error) { return parseLAGs([]wire.TLV{foreign, th(wire.TLVLAG, "0100000003")}) },
			[]LAG{{ID: 1, Ports: []int{1, 2}}}},
		{"storm control", func() (any, error) { return parseStorm([]wire.TLV{foreign}) }, []PortStorm(nil)},
		{"bandwidth", func() (any, error) {
			return parseBandwidth([]wire.TLV{foreign, th(wire.TLVBandwidthEgress, "010000000040")})
		},
			[]PortBandwidth{{Port: 1, EgressKbps: 0x40}}},
		{"mirror", func() (any, error) {
			return parseMirror([]wire.TLV{foreign, th(wire.TLVMirror, "00000000000000000000")})
		},
			Mirror{IngressPorts: []int{}, EgressPorts: []int{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParsersRejectMalformedState(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"mirror without a TLV", second(parseMirror(nil)), "no port mirror TLV"},
		{"QoS mode with two bytes", second(parseQoS([]wire.TLV{{Type: wire.TLVQoSMode, Value: []byte{1, 2}}})), "QoS mode TLV has 2 bytes"},
		{"QoS without any mode", second(parseQoS([]wire.TLV{th(wire.TLVQoSPriority, "0100")})), "no QoS mode"},
		{"802.1Q state with two bytes", second(parseDot1Q([]wire.TLV{{Type: wire.TLVVLAN8021Q, Value: []byte{0, 0}}})), "802.1Q state TLV has 2 bytes"},
		{"802.1Q without any state", second(parseDot1Q(nil)), "no 802.1Q state"},
		{"port-based VLAN state with two bytes", second(parsePortBasedVLAN([]wire.TLV{{Type: wire.TLVVLANPortBased, Value: []byte{1, 1}}})), "port-based VLAN state TLV has 2 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil || !strings.Contains(tt.err.Error(), tt.wantMsg) {
				t.Fatalf("want an error containing %q, got %v", tt.wantMsg, tt.err)
			}
		})
	}
}

// second returns the error of a (value, error) pair.
func second[T any](_ T, err error) error { return err }
