package escp

import (
	"context"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseIGMP(t *testing.T) {
	got, err := parseIGMP([]wire.TLV{tlvHex(t, wire.TLVIGMPEnabled, "01"), tlvHex(t, wire.TLVIGMPSuppression, "00")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, IGMP{Enabled: true, Groups: []IGMPGroup{}}) {
		t.Fatalf("got %+v", got)
	}
	// 239.255.255.250 vlan 1 ports 1 and 4, then 224.0.1.1 vlan 10 port 8
	got, err = parseIGMP([]wire.TLV{tlvHex(t, wire.TLVIGMPGroups, "effffffa000100000009"+"e0000101000a00000080"), tlvHex(t, wire.TLVIGMPSuppression, "01")})
	if err != nil {
		t.Fatal(err)
	}
	want := IGMP{ReportSuppression: true, Groups: []IGMPGroup{
		{IP: "239.255.255.250", VLAN: 1, Ports: []int{1, 4}},
		{IP: "224.0.1.1", VLAN: 10, Ports: []int{8}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, err := parseIGMP([]wire.TLV{{Type: wire.TLVIGMPGroups, Value: make([]byte, 7)}}); err == nil {
		t.Fatal("expected length error")
	}
}

// The state and the report-suppression flag travel as two TLVs in one request. Setting one at a
// time tells a swapped pair apart, which setting both could not.
func TestSetIGMPWire(t *testing.T) {
	cases := []struct {
		name                string
		cfg                 IGMPConfig
		wantState, wantSupp byte
	}{
		{"snooping only", IGMPConfig{Enabled: true}, 1, 0},
		{"suppression only", IGMPConfig{ReportSuppression: true}, 0, 1},
		{"both", IGMPConfig{Enabled: true, ReportSuppression: true}, 1, 1},
		{"neither", IGMPConfig{}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeSwitch(t)
			c := dialFake(t, f, "secret")
			if err := c.SetIGMP(context.Background(), tc.cfg); err != nil {
				t.Fatal(err)
			}
			want := []wire.TLV{
				{Type: wire.TLVIGMPEnabled, Value: []byte{tc.wantState}},
				{Type: wire.TLVIGMPSuppression, Value: []byte{tc.wantSupp}},
			}
			if got := f.set(); !reflect.DeepEqual(got, want) {
				t.Fatalf("wire %+v, want %+v", got, want)
			}
		})
	}
}
