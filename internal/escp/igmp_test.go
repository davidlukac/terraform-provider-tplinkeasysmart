package escp

import (
	"reflect"
	"testing"
)

func TestParseIGMP(t *testing.T) {
	got, err := parseIGMP([]TLV{tlvHex(t, TLVIGMPEnabled, "01"), tlvHex(t, TLVIGMPSuppression, "00")})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, IGMP{Enabled: true, Groups: []IGMPGroup{}}) {
		t.Fatalf("got %+v", got)
	}
	// 239.255.255.250 vlan 1 ports 1 and 4, then 224.0.1.1 vlan 10 port 8
	got, err = parseIGMP([]TLV{tlvHex(t, TLVIGMPGroups, "effffffa000100000009"+"e0000101000a00000080"), tlvHex(t, TLVIGMPSuppression, "01")})
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
	if _, err := parseIGMP([]TLV{{Type: TLVIGMPGroups, Value: make([]byte, 7)}}); err == nil {
		t.Fatal("expected length error")
	}
}
