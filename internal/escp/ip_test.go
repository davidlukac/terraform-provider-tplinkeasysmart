package escp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func cfgOf(ip, mask, gw string) IPConfig {
	return IPConfig{IP: net.ParseIP(ip), Netmask: net.ParseIP(mask), Gateway: net.ParseIP(gw)}
}

func TestValidateIPConfig(t *testing.T) {
	good := []IPConfig{
		cfgOf("192.168.77.13", "255.255.255.0", "192.168.77.1"),
		cfgOf("192.168.77.14", "255.255.0.0", "192.168.77.2"),
		cfgOf("10.0.0.2", "255.0.0.0", "0.0.0.0"),                  // no gateway
		cfgOf("192.168.77.13", "255.255.255.252", "192.168.77.14"), // /30: two hosts
	}
	for _, cfg := range good {
		if err := ValidateIPConfig(cfg); err != nil {
			t.Errorf("%s: %v", cfg, err)
		}
	}
	bad := []struct {
		name string
		cfg  IPConfig
		want string
	}{
		{"missing gateway", IPConfig{IP: net.ParseIP("192.168.77.13"), Netmask: net.ParseIP("255.255.255.0")}, "must all be IPv4"},
		{"ipv6 address", cfgOf("2001:db8::1", "255.255.255.0", "192.168.77.1"), "must all be IPv4"},
		{"gap in mask", cfgOf("192.168.77.13", "255.0.255.0", "192.168.77.1"), "contiguous prefix"},
		{"mask zero", cfgOf("192.168.77.13", "0.0.0.0", "192.168.77.1"), "contiguous prefix"},
		{"mask /7", cfgOf("10.0.0.2", "254.0.0.0", "10.0.0.1"), "contiguous prefix"},
		{"mask /31", cfgOf("192.168.77.13", "255.255.255.254", "192.168.77.12"), "contiguous prefix"},
		{"unspecified address", cfgOf("0.0.0.0", "255.255.255.0", "192.168.77.1"), "not a usable host"},
		{"loopback address", cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2"), "not a usable host"},
		{"multicast address", cfgOf("224.0.0.5", "255.255.255.0", "224.0.0.1"), "not a usable host"},
		{"network address", cfgOf("192.168.77.0", "255.255.255.0", "192.168.77.1"), "not a usable host"},
		{"broadcast address", cfgOf("192.168.77.255", "255.255.255.0", "192.168.77.1"), "not a usable host"},
		{"gateway outside", cfgOf("192.168.77.13", "255.255.255.0", "192.168.78.1"), "gateway"},
		{"gateway is the network", cfgOf("192.168.77.13", "255.255.255.0", "192.168.77.0"), "gateway"},
		{"gateway is the broadcast", cfgOf("192.168.77.13", "255.255.255.0", "192.168.77.255"), "gateway"},
		{"gateway is the switch", cfgOf("192.168.77.13", "255.255.255.0", "192.168.77.13"), "gateway"},
	}
	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIPConfig(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("want an error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestIPConfigFromInfoRefusesUnusableReplies(t *testing.T) {
	if _, err := ipConfigFromInfo(Info{IP: "192.168.77.13", Netmask: "255.255.255.0"}); err == nil {
		t.Fatal("a reply without a gateway must be refused")
	}
	got, err := ipConfigFromInfo(Info{IP: "192.168.77.13", Netmask: "255.255.255.0", Gateway: "192.168.77.1"})
	if err != nil || got.String() != "ip 192.168.77.13 mask 255.255.255.0 gateway 192.168.77.1" {
		t.Fatalf("got %v, %v", got, err)
	}
}

// The fake answers from 127.0.0.1, so the "move" keeps the address and changes
// the mask and gateway; the wire layout is what matters here.
func TestSetIPConfigWireLayoutAndReadBack(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.2")); err != nil {
		t.Fatal(err)
	}
	sets := f.allSets()
	if len(sets) != 1 {
		t.Fatalf("want exactly one write, got %d", len(sets))
	}
	want := []wire.TLV{
		{Type: wire.TLVHostname, Value: []byte("TL-SG108E-fake\x00")}, // kept as the switch reports it
		{Type: wire.TLVDHCP, Value: []byte{1}},                        // kept as the switch reports it
		{Type: wire.TLVIP, Value: []byte{127, 0, 0, 1}}, {Type: wire.TLVNetmask, Value: []byte{255, 255, 0, 0}}, {Type: wire.TLVGateway, Value: []byte{127, 0, 0, 2}},
	}
	if len(sets[0]) != len(want) {
		t.Fatalf("SET carries %d TLVs, want %d: %+v", len(sets[0]), len(want), sets[0])
	}
	for i, w := range want {
		if sets[0][i].Type != w.Type || !bytes.Equal(sets[0][i].Value, w.Value) {
			t.Errorf("TLV %d = %+v, want %+v", i, sets[0][i], w)
		}
	}
	info, err := c.Info(context.Background())
	if err != nil || info.Netmask != "255.255.0.0" || info.Gateway != "127.0.0.2" {
		t.Fatalf("after the move: %+v err=%v", info, err)
	}
}

func TestSetIPConfigValidatesBeforeTheNetwork(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.0.255.0", "127.0.0.2")); err == nil {
		t.Fatal("an invalid mask must be refused")
	}
	if n := len(f.allSets()); n != 0 {
		t.Fatalf("an invalid request must not write, got %d writes", n)
	}
}

func TestSetIPConfigRefusesUnusableCurrentConfiguration(t *testing.T) {
	f := startFakeSwitch(t)
	f.configure(func(f *fakeSwitch) {
		f.discoverTLVs = []wire.TLV{{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVMAC, Value: []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}}
	})
	c := dialFake(t, f, "secret")
	err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2"))
	if err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
		t.Fatalf("want a refusal, got %v", err)
	}
	if n := len(f.allSets()); n != 0 {
		t.Fatalf("nothing may be written, got %d writes", n)
	}
}

func TestSetIPConfigErrorsDoNotMoveTheClient(t *testing.T) {
	t.Run("silent switch", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.goMute()
		c := dialFast(t, f, "secret")
		if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2")); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
	})
	t.Run("wrong credentials", func(t *testing.T) {
		f := startFakeSwitch(t)
		c := dialFake(t, f, "wrong")
		if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.255.0", "127.0.0.2")); !errors.Is(err, ErrRejected) {
			t.Fatalf("want ErrRejected, got %v", err)
		}
		if !c.dest.IP.Equal(net.ParseIP("127.0.0.1")) || c.alsoFrom != nil {
			t.Fatalf("a rejected write must leave the client where it was: dest=%v alsoFrom=%v", c.dest.IP, c.alsoFrom)
		}
	})
}

func TestSetIPConfigReportsAnUnconfirmedMove(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFast(t, f, "secret")
	c.moveWait = 300 * time.Millisecond
	// 127.0.0.2 is where the client looks after the write; the fake stays at 127.0.0.1.
	err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.2", "255.255.255.0", "127.0.0.3"))
	if !errors.Is(err, ErrMoveUnconfirmed) {
		t.Fatalf("want ErrMoveUnconfirmed, got %v", err)
	}
	if !c.dest.IP.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("the client must follow the switch to the new address, dest=%v", c.dest.IP)
	}
}

func TestAcceptsRepliesFromTheNewAddressWhileMoving(t *testing.T) {
	c, err := Dial(Config{Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	data := wire.RC4Static(wire.Marshal(wire.Header{Op: wire.OpSetReply, Seq: c.seq}, nil))
	from := &net.UDPAddr{IP: net.ParseIP("127.0.0.2")}
	if _, ok := c.accept(from, data); ok {
		t.Fatal("a reply from another address must be dropped normally")
	}
	c.alsoFrom = net.ParseIP("127.0.0.2")
	if _, ok := c.accept(from, data); !ok {
		t.Fatal("a reply from the address being moved to must be accepted")
	}
}

func TestSetIPConfigWaitsUntilTheSwitchAnswersAtTheNewAddress(t *testing.T) {
	f := startFakeSwitch(t)
	f.configure(func(f *fakeSwitch) { f.staleAfterSet = 2 }) // two probes still see the old address
	c := dialFake(t, f, "secret")
	start := time.Now()
	if err := c.SetIPConfig(context.Background(), cfgOf("127.0.0.1", "255.255.0.0", "127.0.0.2")); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < ipMovePoll {
		t.Fatalf("SetIPConfig returned after %s: it must keep probing until the switch reports the new address", time.Since(start))
	}
	if info, err := c.Info(context.Background()); err != nil || info.Netmask != "255.255.0.0" {
		t.Fatalf("after the move: %+v err=%v", info, err)
	}
}
