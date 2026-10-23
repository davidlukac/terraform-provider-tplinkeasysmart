package escp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestSetHostnameRefusesUnusableIPConfiguration(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.DiscoverReply = []escptest.Setting{{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVHostname, Value: []byte("TL-SG108E-fake\x00")}, {Type: wire.TLVMAC, Value: []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}}
	})
	c := dialFake(t, f, "secret")
	err := c.SetHostname(context.Background(), "sw01")
	if err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
		t.Fatalf("want a refusal because the IP config is unusable, got %v", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("nothing may be written when the IP config cannot be re-sent, got %d writes", n)
	}
}

func TestSetHostnameFailsWhenSwitchIsSilent(t *testing.T) {
	f := escptest.Start(t)
	f.Mute()
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 0
	c.cfg.Timeout = 100 * time.Millisecond
	if err := c.SetHostname(context.Background(), "sw01"); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply from the discovery step, got %v", err)
	}
}

func TestSetHostnameRejectsWrongCredentials(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "wrong")
	if err := c.SetHostname(context.Background(), "sw01"); !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestSendFailureOnClosedSocketAffectsReadsAndBackup(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if _, err := c.GetLED(context.Background()); err != nil { // opens the socket and establishes the session
		t.Fatal(err)
	}
	if err := c.conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "send") {
		t.Fatalf("want a send error on a closed socket, got %v", err)
	}
	if _, err := c.PortStatistics(context.Background()); err == nil {
		t.Fatal("a read on a closed socket must fail")
	}
	if _, err := c.ConfigBackup(context.Background()); err == nil {
		t.Fatal("a backup on a closed socket must fail")
	}
}

func TestBindFailureIsReported(t *testing.T) {
	taken, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	port := taken.LocalAddr().(*net.UDPAddr).Port
	c, err := Dial(Config{Host: "127.0.0.1", HostPort: port, Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "binding UDP port") {
		t.Fatalf("want a bind error naming the port, got %v", err)
	}
	if _, err := c.ConfigBackup(context.Background()); err == nil {
		t.Fatal("backup must fail when the socket cannot be opened")
	}
}
