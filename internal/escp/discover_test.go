package escp

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// discoverTLVs is the body of a discovery reply for a switch with the given MAC, address and name.
func discoverTLVs(mac [6]byte, ip [4]byte, hostname string) []wire.TLV {
	return []wire.TLV{
		{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVHostname, Value: []byte(hostname + "\x00")},
		{Type: wire.TLVMAC, Value: mac[:]}, {Type: wire.TLVIP, Value: ip[:]},
		{Type: wire.TLVNetmask, Value: []byte{255, 255, 255, 0}}, {Type: wire.TLVGateway, Value: []byte{192, 0, 2, 1}},
	}
}

// packet builds a datagram as a switch sends it: static RC4 over header and TLVs.
func packet(op byte, seq uint16, tlvs []wire.TLV) []byte {
	return wire.RC4Static(wire.Marshal(wire.Header{Op: op, SwitchMAC: [6]byte{0, 0, 0x5e, 0, 0x53, 1}, Seq: seq}, tlvs))
}

// responder is a UDP socket on loopback that stands in for the switches on a network. It
// records the header of every discovery probe and answers each with the datagrams that
// replies returns, sent to the host port like a real switch does.
type responder struct {
	port   int
	probes chan wire.Header
}

func startResponder(t *testing.T, hostPort int, replies func(h wire.Header) [][]byte) *responder {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	r := &responder{port: conn.LocalAddr().(*net.UDPAddr).Port, probes: make(chan wire.Header, 64)}
	go func() {
		buf := make([]byte, maxDatagram)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			h, _, err := wire.Unmarshal(wire.RC4Static(buf[:n]))
			if err != nil || h.Op != wire.OpDiscovery {
				continue
			}
			r.probes <- h
			if replies == nil {
				continue
			}
			for _, d := range replies(h) {
				_, _ = conn.WriteToUDP(d, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: hostPort})
			}
		}
	}()
	return r
}

// next returns the next probe the responder saw, failing the test instead of hanging when none comes.
func (r *responder) next(t *testing.T) wire.Header {
	t.Helper()
	select {
	case h := <-r.probes:
		return h
	case <-time.After(5 * time.Second):
		t.Fatal("the responder saw fewer probes than expected")
		return wire.Header{}
	}
}

// loopbackEnv aims discovery at the responder through made-up interfaces.
func loopbackEnv(t *testing.T, r *responder, hostPort int, ifaces ...netInterface) discoverEnv {
	t.Helper()
	return discoverEnv{
		interfaces: func() ([]netInterface, error) { return ifaces, nil },
		switchPort: r.port, hostPort: hostPort,
	}
}

var (
	macOne = MAC{2, 0, 0, 0, 0, 1}
	macTwo = MAC{2, 0, 0, 0, 0, 2}
	lo     = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
)

func TestDiscoverReturnsEveryDistinctSwitchSortedByAddress(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	macA, macB := [6]byte{0xa8, 0x42, 0xa1, 0, 0, 0x30}, [6]byte{0xc0, 0x3a, 0x55, 0, 0, 0x09}
	r := startResponder(t, hostPort, func(h wire.Header) [][]byte {
		noMAC := []wire.TLV{{Type: wire.TLVHostname, Value: []byte("no-mac\x00")}, {Type: wire.TLVIP, Value: []byte{192, 0, 2, 50}}}
		return [][]byte{
			packet(wire.OpReply, h.Seq, discoverTLVs(macA, [4]byte{192, 0, 2, 30}, "sw-a")),
			packet(wire.OpReply, h.Seq, discoverTLVs(macB, [4]byte{192, 0, 2, 9}, "sw-b")),
			packet(wire.OpReply, h.Seq, discoverTLVs(macA, [4]byte{192, 0, 2, 30}, "sw-a")), // duplicate
			[]byte("not an ESCP packet"),
			packet(wire.OpReply, (h.Seq+1)%1000, discoverTLVs([6]byte{9, 9, 9, 9, 9, 9}, [4]byte{192, 0, 2, 99}, "other-request")),
			packet(wire.OpDiscovery, h.Seq, discoverTLVs([6]byte{8, 8, 8, 8, 8, 8}, [4]byte{192, 0, 2, 98}, "echo")),
			packet(wire.OpReply, h.Seq, noMAC), // no MAC: keyed by the sender's address
		}
	})
	got, err := discoverWith(context.Background(), DiscoverOptions{Window: 300 * time.Millisecond},
		loopbackEnv(t, r, hostPort, netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(got))
	for i, info := range got {
		names[i] = info.IP.String() + " " + info.Hostname.String()
	}
	// Numeric order (.9 before .30), one entry per MAC, nothing from the other request,
	// the echo or the garbage; the reply without a MAC appears once.
	want := []string{"192.0.2.9 sw-b", "192.0.2.30 sw-a", "192.0.2.50 no-mac"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	if got[0].MAC.String() != "c0:3a:55:00:00:09" || got[0].Model != "TL-SG108E" || got[0].Gateway.String() != "192.0.2.1" {
		t.Errorf("fields not decoded: %+v", got[0])
	}
}

// The probe is sent again halfway through the window, carrying the same sequence number, and
// each interface puts its own MAC in it.
func TestDiscoverProbesEveryInterfaceTwice(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, nil)
	_, err := discoverWith(context.Background(), DiscoverOptions{Window: 200 * time.Millisecond}, loopbackEnv(t, r, hostPort,
		netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}, netInterface{Name: "en1", MAC: macTwo, Broadcasts: lo}))
	if err != nil {
		t.Fatal(err)
	}
	macs := map[[6]byte]int{}
	var seq uint16
	for i := 0; i < 4; i++ { // two interfaces, two rounds; blocks until the responder has seen them
		h := r.next(t)
		macs[h.HostMAC]++
		if i == 0 {
			seq = h.Seq
			if seq < discoverySeqBase || seq >= discoverySeqBase+discoverySeqSpace {
				t.Errorf("the probe's sequence number %d is outside the discovery range", seq)
			}
		}
		if h.Seq != seq {
			t.Errorf("probe %d has sequence %d, want %d", i, h.Seq, seq)
		}
		if h.SwitchMAC != ([6]byte{}) {
			t.Errorf("a discovery probe names no switch, got %x", h.SwitchMAC)
		}
	}
	if macs[macOne] != 2 || macs[macTwo] != 2 {
		t.Fatalf("probes per host MAC: %v", macs)
	}
}

func TestDiscoverCanBeLimitedToOneInterface(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, nil)
	_, err := discoverWith(context.Background(), DiscoverOptions{Interface: "en1", Window: 100 * time.Millisecond}, loopbackEnv(t, r, hostPort,
		netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}, netInterface{Name: "en1", MAC: macTwo, Broadcasts: lo}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if h := r.next(t); MAC(h.HostMAC) != macTwo {
			t.Fatalf("probe %d came from %x, want only en1 (%x)", i, h.HostMAC, macTwo)
		}
	}
	select {
	case h := <-r.probes:
		t.Fatalf("an unrequested interface sent a probe: %x", h.HostMAC)
	default:
	}
}

func TestDiscoverWithNoAnswerIsEmptyNotAnError(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, nil)
	got, err := discoverWith(context.Background(), DiscoverOptions{Window: 100 * time.Millisecond},
		loopbackEnv(t, r, hostPort, netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}))
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, %v; want an empty, non-nil list and no error", got, err)
	}
}

func TestDiscoverStopsWhenTheContextIsCancelled(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := startResponder(t, hostPort, func(wire.Header) [][]byte { cancel(); return nil })
	guard, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	start := time.Now()
	got, err := discoverWith(ctx, DiscoverOptions{Window: time.Minute}, loopbackEnv(t, r, hostPort,
		netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}))
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("got %v, %v; want no replies and context.Canceled", got, err)
	}
	if guard.Err() != nil || time.Since(start) > 5*time.Second {
		t.Fatalf("cancellation did not end the wait: took %v", time.Since(start))
	}
}

func TestDiscoverWithACancelledContextReturnsAtOnce(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := discoverWith(ctx, DiscoverOptions{Window: time.Minute}, loopbackEnv(t, r, hostPort,
		netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}))
	if !errors.Is(err, context.Canceled) || time.Since(start) > 5*time.Second {
		t.Fatalf("err = %v after %v", err, time.Since(start))
	}
}

func TestDiscoverSurvivesOneInterfaceThatCannotSend(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, func(h wire.Header) [][]byte {
		return [][]byte{packet(wire.OpReply, h.Seq, discoverTLVs([6]byte{1, 2, 3, 4, 5, 6}, [4]byte{192, 0, 2, 7}, "sw"))}
	})
	env := loopbackEnv(t, r, hostPort,
		netInterface{Name: "bad", MAC: macOne, Broadcasts: []netip.Addr{netip.MustParseAddr("192.0.2.255")}},
		netInterface{Name: "en0", MAC: macTwo, Broadcasts: lo})
	env.write = func(conn *net.UDPConn, pkt []byte, to *net.UDPAddr) error {
		if to.IP.Equal(net.IPv4(192, 0, 2, 255)) {
			return errors.New("network is unreachable")
		}
		_, err := conn.WriteToUDP(pkt, to)
		return err
	}
	got, err := discoverWith(context.Background(), DiscoverOptions{Window: 200 * time.Millisecond}, env)
	if err != nil || len(got) != 1 || got[0].Hostname != "sw" {
		t.Fatalf("got %+v, %v; one failing interface must not hide the others", got, err)
	}
}

func TestDiscoverFailsWhenNoProbeCanBeSent(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, nil)
	cause := errors.New("network is unreachable")
	env := loopbackEnv(t, r, hostPort, netInterface{Name: "en0", MAC: macOne, Broadcasts: lo})
	env.write = func(*net.UDPConn, []byte, *net.UDPAddr) error { return cause }
	_, err := discoverWith(context.Background(), DiscoverOptions{Window: 100 * time.Millisecond}, env)
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "discovery probe") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverReportsABusyReplyPort(t *testing.T) {
	busy, err := net.ListenUDP("udp4", &net.UDPAddr{Port: escptest.FreeUDPPort(t)}) // does not share the port
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.LocalAddr().(*net.UDPAddr).Port
	_, err = discoverWith(context.Background(), DiscoverOptions{Window: 50 * time.Millisecond}, discoverEnv{
		interfaces: func() ([]netInterface, error) {
			return []netInterface{{Name: "en0", MAC: macOne, Broadcasts: lo}}, nil
		},
		switchPort: 1, hostPort: port,
	})
	if err == nil || !strings.Contains(err.Error(), "binding UDP port") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverRefusesWhatCannotWork(t *testing.T) {
	good := func() ([]netInterface, error) {
		return []netInterface{{Name: "en0", MAC: macOne, Broadcasts: lo}}, nil
	}
	cause := errors.New("boom")
	cases := []struct {
		name    string
		opts    DiscoverOptions
		ifaces  func() ([]netInterface, error)
		is      error
		message string
	}{
		{"negative window", DiscoverOptions{Window: -time.Second}, good, nil, "negative discovery window"},
		{"interfaces cannot be listed", DiscoverOptions{}, func() ([]netInterface, error) { return nil, cause }, cause, ""},
		{"no usable interface", DiscoverOptions{}, func() ([]netInterface, error) { return nil, nil }, ErrNoInterface, ""},
		{"interface not usable", DiscoverOptions{Interface: "en7"}, good, ErrNoInterface, `"en7"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := discoverWith(context.Background(), tc.opts, discoverEnv{interfaces: tc.ifaces, switchPort: 1, hostPort: escptest.FreeUDPPort(t)})
			if err == nil || (tc.is != nil && !errors.Is(err, tc.is)) || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

// Discover itself is wired to this host; whatever the machine, an interface that does not
// exist is reported as such before any packet is sent.
func TestDiscoverUnknownInterfaceOnThisHost(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{Interface: "no-such-interface-for-escp-tests"})
	if !errors.Is(err, ErrNoInterface) {
		t.Fatalf("err = %v", err)
	}
}

func TestUsableInterfaces(t *testing.T) {
	up := net.FlagUp | net.FlagBroadcast
	mac := net.HardwareAddr{2, 0, 0, 0, 0, 1}
	ipnet := func(cidr string) net.Addr {
		ip, n, err := net.ParseCIDR(cidr)
		if err != nil {
			t.Fatal(err)
		}
		n.IP = ip
		return n
	}
	addrs := map[string][]net.Addr{
		"en0":   {ipnet("192.168.1.5/24"), ipnet("fe80::1/64"), ipnet("10.0.0.2/16")},
		"lo0":   {ipnet("127.0.0.1/8")},
		"down":  {ipnet("192.168.2.5/24")},
		"nobc":  {ipnet("192.168.3.5/24")},
		"nomac": {ipnet("192.168.4.5/24")},
		"v6":    {ipnet("fe80::2/64")},
		"p2p":   {ipnet("192.168.5.5/32")},
	}
	all := []net.Interface{
		{Name: "en0", Flags: up, HardwareAddr: mac},
		{Name: "lo0", Flags: up | net.FlagLoopback, HardwareAddr: mac},
		{Name: "down", Flags: net.FlagBroadcast, HardwareAddr: mac},
		{Name: "nobc", Flags: net.FlagUp, HardwareAddr: mac},
		{Name: "nomac", Flags: up},
		{Name: "v6", Flags: up, HardwareAddr: mac},
		{Name: "p2p", Flags: up, HardwareAddr: mac},
		{Name: "broken", Flags: up, HardwareAddr: mac},
	}
	got := usableInterfaces(all, func(i net.Interface) ([]net.Addr, error) {
		if i.Name == "broken" {
			return nil, errors.New("no addresses")
		}
		return addrs[i.Name], nil
	})
	want := []netInterface{{Name: "en0", MAC: macOne, Broadcasts: []netip.Addr{netip.MustParseAddr("192.168.1.255"), netip.MustParseAddr("10.0.255.255")}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestSubnetBroadcast(t *testing.T) {
	mask24 := net.CIDRMask(24, 32)
	cases := []struct {
		name string
		addr net.Addr
		want string // "" = none
	}{
		{"/24", &net.IPNet{IP: net.IPv4(192, 168, 1, 5), Mask: mask24}, "192.168.1.255"},
		{"/16 given as a 16-byte address", &net.IPNet{IP: net.IPv4(10, 1, 2, 3).To16(), Mask: net.CIDRMask(16, 32)}, "10.1.255.255"},
		{"/30", &net.IPNet{IP: net.IPv4(192, 0, 2, 1), Mask: net.CIDRMask(30, 32)}, "192.0.2.3"},
		{"/31 has none", &net.IPNet{IP: net.IPv4(192, 0, 2, 1), Mask: net.CIDRMask(31, 32)}, ""},
		{"/32 has none", &net.IPNet{IP: net.IPv4(192, 0, 2, 1), Mask: net.CIDRMask(32, 32)}, ""},
		{"IPv6", &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}, ""},
		{"not a network", &net.IPAddr{IP: net.IPv4(192, 0, 2, 1)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subnetBroadcast(tc.addr)
			if tc.want == "" {
				if got.IsValid() {
					t.Fatalf("got %v, want none", got)
				}
				return
			}
			if got.String() != tc.want {
				t.Fatalf("got %v, want %s", got, tc.want)
			}
		})
	}
}

func TestSortedInfosOrdersByAddressThenMAC(t *testing.T) {
	got := sortedInfos(map[string]Info{
		"a": {IP: netip.MustParseAddr("192.0.2.10"), MAC: MAC{0xbb}}, "b": {IP: netip.MustParseAddr("192.0.2.10"), MAC: MAC{0xaa}}, "c": {IP: netip.MustParseAddr("192.0.2.9"), MAC: MAC{0xcc}}, "d": {IP: netip.Addr{}, MAC: MAC{0xdd}},
	})
	var order []string
	for _, i := range got {
		order = append(order, hex.EncodeToString(i.MAC[:1]))
	}
	if want := []string{"dd", "cc", "aa", "bb"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order %v, want %v", order, want)
	}
}
