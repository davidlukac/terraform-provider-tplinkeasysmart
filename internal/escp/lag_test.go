package escp

import (
	"context"
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

func TestSetLAGWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetLAG(ctx, 2, []int{6, 7}); err != nil {
		t.Fatal(err)
	}
	if got := f.set(); got[0].Type != TLVLAG || !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0x60}) {
		t.Fatalf("wire %+v", got)
	}
	if err := c.SetLAG(ctx, 2, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.set(); !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0}) {
		t.Fatalf("delete wire %v", got[0].Value)
	}
	n := len(f.allSets())
	if c.SetLAG(ctx, 0, []int{1}) == nil || c.SetLAG(ctx, 2, []int{40}) == nil {
		t.Fatal("invalid group or port must be rejected")
	}
	if len(f.allSets()) != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}
