package escp

import (
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParsePortStats(t *testing.T) {
	got, err := parsePortStats([]wire.TLV{
		tlvHex(t, wire.TLVPortStats, "0101061434f1b7000000002873fb8a00000000"),
		tlvHex(t, wire.TLVPortStats, "03010000000000000000000000000000000000"),
		tlvHex(t, wire.TLVPortStats, "08010637b1c885000000001b4ae3f300000001"),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []PortStats{
		{ID: 1, Enabled: true, Link: "1000Mbps-full", TxGood: 0x1434f1b7, RxGood: 0x2873fb8a},
		{ID: 3, Enabled: true, Link: "link-down"},
		{ID: 8, Enabled: true, Link: "1000Mbps-full", TxGood: 0x37b1c885, RxGood: 0x1b4ae3f3, RxBad: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, err := parsePortStats([]wire.TLV{{Type: wire.TLVPortStats, Value: make([]byte, 5)}}); err == nil {
		t.Fatal("expected error for short TLV")
	}
}

// The row's port is a PortID, so it feeds the port types without a conversion: the ports of a
// statistics read are a PortSet, and a row names a port the Port Setting read lists too.
func TestPortStatsIDsAreUsablePortIDs(t *testing.T) {
	rows, err := parsePortStats([]wire.TLV{
		tlvHex(t, wire.TLVPortStats, "0101061434f1b7000000002873fb8a00000000"),
		tlvHex(t, wire.TLVPortStats, "03010000000000000000000000000000000000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := NewPortSet(rows[0].ID, rows[1].ID)
	if err != nil || set != MustPortSet(1, 3) || !set.Has(rows[1].ID) {
		t.Fatalf("got %v, %v; want ports 1,3", set, err)
	}
}
