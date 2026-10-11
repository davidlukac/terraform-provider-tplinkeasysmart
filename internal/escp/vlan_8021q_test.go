package escp

import (
	"reflect"
	"testing"
)

func TestParseDot1QDisabled(t *testing.T) {
	// captured from a TL-SG108E v6 (fw 20230218): 802.1Q off, 32 VLANs max, every port PVID 1
	var tl []TLV
	tl = append(tl, tlvHex(t, TLVVLAN8021Q, "00"), tlvHex(t, TLVVLAN8021QMax, "20"))
	for p := 1; p <= 8; p++ {
		tl = append(tl, TLV{Type: TLVVLAN8021QPVID, Value: []byte{byte(p), 0, 1}})
	}
	got, err := parseDot1Q(tl)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.MaxVLAN != 32 || len(got.VLANs) != 0 || len(got.PVIDs) != 8 || got.PVIDs[7] != (PortPVID{Port: 8, PVID: 1}) {
		t.Fatalf("got %+v", got)
	}
}

func TestParseDot1QVLANs(t *testing.T) {
	// layout from the vendor code: VLAN 10 "iot", members 1,2,8, tagged 8
	got, err := parseDot1Q([]TLV{
		tlvHex(t, TLVVLAN8021Q, "01"),
		tlvHex(t, TLVVLAN8021QVLAN, "000a"+"00000083"+"00000080"+"696f7400"),
		tlvHex(t, TLVVLAN8021QVLAN, "0001"+"000000ff"+"00000000"+"44656661756c7400"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []VLAN8021Q{
		{ID: 10, Name: "iot", Tagged: []int{8}, Untagged: []int{1, 2}},
		{ID: 1, Name: "Default", Tagged: []int{}, Untagged: []int{1, 2, 3, 4, 5, 6, 7, 8}},
	}
	if !reflect.DeepEqual(got.VLANs, want) || !got.Enabled {
		t.Fatalf("got %+v", got)
	}
	if _, err := parseDot1Q([]TLV{tlvHex(t, TLVVLAN8021Q, "01"), {Type: TLVVLAN8021QVLAN, Value: []byte{0, 1}}}); err == nil {
		t.Fatal("expected short entry error")
	}
	if _, err := parseDot1Q([]TLV{{Type: TLVVLAN8021QPVID, Value: []byte{1}}}); err == nil {
		t.Fatal("expected error (bad pvid / no state)")
	}
}
