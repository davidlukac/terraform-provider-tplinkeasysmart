package escp

import (
	"reflect"
	"testing"
)

func TestParseStorm(t *testing.T) {
	got, err := parseStorm([]TLV{
		tlvHex(t, TLVStormControl, "010000000000000000"), // captured: everything off
		tlvHex(t, TLVStormControl, "0200010100"+"00001388"),
		tlvHex(t, TLVStormControl, "0300010100"+"00000000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// entries 2 and 3: unknown-unicast on, multicast on, broadcast off; entry 3 has no rate so it is not active
	want := []PortStorm{
		{Port: 1},
		{Port: 2, UnknownUnicast: true, Multicast: true, RateKbps: 0x1388, Enabled: true},
		{Port: 3, UnknownUnicast: true, Multicast: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, err := parseStorm([]TLV{{Type: TLVStormControl, Value: []byte{1}}}); err == nil {
		t.Fatal("expected length error")
	}
}
