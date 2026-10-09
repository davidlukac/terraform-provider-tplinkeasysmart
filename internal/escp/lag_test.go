package escp

import (
	"reflect"
	"testing"
)

func TestParseLAGs(t *testing.T) {
	got, err := parseLAGs([]TLV{tlvHex(t, TLVLAG, "0100000003"), tlvHex(t, TLVLAG, "0200000000"), tlvHex(t, TLVLAG, "0380000005")})
	if err != nil {
		t.Fatal(err)
	}
	// 0x80000005 -> bits 0,2,31 -> ports 1,3,32
	want := []LAG{{ID: 1, Ports: []int{1, 2}}, {ID: 2, Ports: []int{}}, {ID: 3, Ports: []int{1, 3, 32}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if _, err := parseLAGs([]TLV{{Type: TLVLAG, Value: []byte{1}}}); err == nil {
		t.Fatal("expected error")
	}
}
