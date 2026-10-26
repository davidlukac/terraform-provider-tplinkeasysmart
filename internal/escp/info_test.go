package escp

import (
	"context"
	"net/netip"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestInfoFromReplyToleratesMissingAndMalformedFields(t *testing.T) {
	if got := infoFromReply(reply{}); got != (Info{}) {
		t.Fatalf("an empty reply must give an empty Info, got %+v", got)
	}
	got := infoFromReply(reply{tlvs: []wire.TLV{
		{Type: wire.TLVIP, Value: []byte{192, 168, 1}},           // 3 bytes: not an address
		{Type: wire.TLVNetmask, Value: []byte{255, 255, 255, 0}}, // fine
		{Type: wire.TLVMAC, Value: []byte{1, 2, 3}},              // 3 bytes: not a MAC
		{Type: wire.TLVDHCP, Value: []byte{1, 1}},                // 2 bytes: ignored
		{Type: wire.TLVFactoryDefault, Value: nil},               // empty: ignored
		{Type: wire.TLVModel, Value: []byte("TL-SG108E")},        // no NUL terminator
	}})
	want := Info{Model: "TL-SG108E", Netmask: netip.MustParseAddr("255.255.255.0")}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestInfo(t *testing.T) {
	c := dialFake(t, escptest.Start(t), "secret")
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.Model != "TL-SG108E" || info.Hostname != "TL-SG108E-fake" || info.MAC.String() != "00:00:5e:00:53:02" ||
		info.Hardware != "TL-SG108E 6.0" || info.IP.String() != "127.0.0.1" || info.DHCP || !info.FactoryDefault {
		t.Fatalf("unexpected info: %+v", info)
	}
}
