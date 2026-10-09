package escp

import (
	"context"
	"reflect"
	"testing"
)

func TestParseQoS(t *testing.T) {
	// captured from switch 01: mode 2 (DSCP/802.1p), all ports raw priority 0 = "1 (lowest)"
	tl := []TLV{tlvHex(t, TLVQoSMode, "02")}
	for p := 1; p <= 3; p++ {
		tl = append(tl, TLV{Type: TLVQoSPriority, Value: []byte{byte(p), 0}})
	}
	tl = append(tl, TLV{Type: TLVQoSPriority, Value: []byte{4, 3}})
	got, err := parseQoS(tl)
	if err != nil {
		t.Fatal(err)
	}
	want := QoS{Mode: 2, ModeName: "dscp-802.1p", Priorities: []PortPriority{{1, 1}, {2, 1}, {3, 1}, {4, 4}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if _, err := parseQoS([]TLV{{Type: TLVQoSMode, Value: []byte{9}}, {Type: TLVQoSPriority, Value: []byte{1}}}); err == nil {
		t.Fatal("expected error for short priority")
	}
	if q, err := parseQoS([]TLV{tlvHex(t, TLVQoSMode, "09")}); err != nil || q.ModeName != "unknown(9)" {
		t.Fatalf("unknown mode: %+v %v", q, err)
	}
}

func TestSetQoSWire(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetQoSMode(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.set(); len(got) != 1 || got[0].Type != TLVQoSMode || got[0].Value[0] != 1 {
		t.Fatalf("mode wire: %+v", got)
	}
	if err := c.SetQoSMode(ctx, 7); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
	if err := c.SetPortPriorities(ctx, map[int]int{3: 4}); err != nil {
		t.Fatal(err)
	}
	got := f.set()
	if len(got) != 8 {
		t.Fatalf("want all 8 ports re-sent, got %d TLVs", len(got))
	}
	for i, tl := range got {
		want := byte(0)
		if i == 2 {
			want = 3 // port 3 -> priority 4 -> raw 3
		}
		if tl.Type != TLVQoSPriority || tl.Value[0] != byte(i+1) || tl.Value[1] != want {
			t.Fatalf("entry %d: %+v", i, tl)
		}
	}
	if err := c.SetPortPriorities(ctx, map[int]int{3: 9}); err == nil {
		t.Fatal("priority 9 must be rejected")
	}
	if err := c.SetPortPriorities(ctx, map[int]int{99: 2}); err == nil {
		t.Fatal("unknown port must be rejected")
	}
}
