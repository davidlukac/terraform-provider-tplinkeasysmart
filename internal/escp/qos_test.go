package escp

import (
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
