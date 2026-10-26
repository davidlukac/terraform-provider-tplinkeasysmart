//go:build linux

package udp

import (
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Linux only: macOS has no loopback broadcast address, so the same fact was measured there with the
// switch itself (two sharing sockets both received every reply).
// The switch answers to the broadcast address (two sharing sockets on macOS both received every
// reply, which a unicast reply would not do there). The same must hold wherever the provider
// runs: a datagram sent to the broadcast address reaches every socket that shares the reply port,
// so several clients and Discover can listen side by side.
func TestSharingSocketsAllReceiveABroadcast(t *testing.T) {
	lc := net.ListenConfig{Control: Share}
	var socks []net.PacketConn
	first, err := lc.ListenPacket(t.Context(), "udp4", ":0")
	if err != nil {
		t.Fatal(err)
	}
	socks = append(socks, first)
	port := first.LocalAddr().(*net.UDPAddr).Port
	for range 2 {
		c, err := lc.ListenPacket(t.Context(), "udp4", net.JoinHostPort("", strconv.Itoa(port)))
		if err != nil {
			t.Fatal(err)
		}
		socks = append(socks, c)
	}
	defer func() {
		for _, s := range socks {
			_ = s.Close() // test cleanup
		}
	}()

	sender, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	raw, err := sender.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var optErr error
	if err := raw.Control(func(fd uintptr) { optErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1) }); err != nil || optErr != nil {
		t.Fatalf("SO_BROADCAST: %v %v", err, optErr)
	}
	if _, err := sender.WriteToUDP([]byte("reply"), &net.UDPAddr{IP: net.IPv4(127, 255, 255, 255), Port: port}); err != nil {
		t.Skipf("this system cannot send to the loopback broadcast address: %v", err)
	}
	for i, s := range socks {
		if err := s.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 16)
		n, _, err := s.ReadFrom(buf)
		if err != nil || string(buf[:n]) != "reply" {
			t.Errorf("socket %d did not receive the broadcast: %q, %v", i+1, buf[:n], err)
		}
	}
}
