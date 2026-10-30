package escp

import (
	"context"
	"net"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// discoveryShapedReply is what a switch answers to a discovery probe: an ordinary reply (the
// operation is the same as the answer to a read) carrying its identity TLVs, with the given
// sequence number. A hardware regression run read one of these as the answer to a LED read,
// a mirror read and a LAG read.
func discoveryShapedReply(c *Client, seq uint16) []byte {
	return wire.RC4Static(wire.Marshal(wire.Header{Op: wire.OpReply, SwitchMAC: c.swMAC, Seq: seq},
		[]wire.TLV{{Type: 1, Value: []byte("TL-SG108E")}, {Type: 2, Value: []byte("switch")}, {Type: 3, Value: []byte{1, 2, 3, 4, 5, 6}}}))
}

// leaveInTheSocket delivers a datagram to the client's socket from the switch's address, as a reply
// that was meant for another client of the same process (or for an earlier request) does.
func leaveInTheSocket(t *testing.T, c *Client, data []byte) {
	t.Helper()
	other, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := other.WriteToUDP(data, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: c.conn.LocalAddr().(*net.UDPAddr).Port}); err != nil {
		t.Fatal(err)
	}
}

func TestAStaleReplyInTheSocketIsNotTheAnswerToTheNextRequest(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	want, err := c.LED(ctx) // opens the session; the switch's MAC is known now
	if err != nil {
		t.Fatal(err)
	}
	leaveInTheSocket(t, c, discoveryShapedReply(c, nextSeq(c.seq))) // carries the sequence number the next request will use
	got, err := c.LED(ctx)
	if err != nil {
		t.Fatalf("the stale reply was taken for the answer: %v", err)
	}
	if got != want {
		t.Fatalf("LED = %t, want %t", got, want)
	}
}

func TestAClientDiscoveryDrawsItsSequenceNumberFromTheDiscoveryRange(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if _, err := c.Info(ctx); err != nil {
		t.Fatal(err)
	}
	if c.seq < discoverySeqBase || c.seq >= discoverySeqBase+discoverySeqSpace {
		t.Fatalf("discovery used sequence number %d, want one of %d-%d, which no request uses", c.seq, discoverySeqBase, discoverySeqBase+discoverySeqSpace-1)
	}
	if _, err := c.LED(ctx); err != nil {
		t.Fatal(err)
	}
	if c.seq >= clientSeqSpace {
		t.Fatalf("a request used sequence number %d, want one below %d", c.seq, clientSeqSpace)
	}
}

func TestAFloodOfStaleDatagramsDoesNotHoldARequestBack(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	want, err := c.LED(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2*maxDiscarded; i++ { // none carries the number of the next request
		leaveInTheSocket(t, c, discoveryShapedReply(c, discoverySeqBase+uint16(i)))
	}
	got, err := c.LED(ctx)
	if err != nil || got != want {
		t.Fatalf("LED = %t, %v; want %t, nil", got, err, want)
	}
}
