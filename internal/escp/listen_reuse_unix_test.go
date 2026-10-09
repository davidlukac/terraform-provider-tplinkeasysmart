//go:build linux || darwin || freebsd

package escp

import (
	"errors"
	"net"
	"syscall"
	"testing"
)

// fakeRawConn lets the socket-option helper be driven without a real socket.
type fakeRawConn struct {
	controlErr error
	fd         uintptr
}

func (f fakeRawConn) Control(fn func(fd uintptr)) error {
	if f.controlErr != nil {
		return f.controlErr
	}
	fn(f.fd)
	return nil
}
func (fakeRawConn) Read(func(uintptr) bool) error  { return nil }
func (fakeRawConn) Write(func(uintptr) bool) error { return nil }

var _ syscall.RawConn = fakeRawConn{}

func TestShareSocketSetsTheOptionsOnARealSocket(t *testing.T) {
	// A listener opened with shareSocket must allow a second listener on the same port.
	lc := net.ListenConfig{Control: shareSocket}
	first, err := lc.ListenPacket(t.Context(), "udp4", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	port := first.LocalAddr().(*net.UDPAddr).Port
	second, err := lc.ListenPacket(t.Context(), "udp4", net.JoinHostPort("", itoa(port)))
	if err != nil {
		t.Fatalf("a second sharing listener on port %d must be allowed: %v", port, err)
	}
	second.Close()
	// ...but not a listener that does not share.
	if plain, err := net.ListenPacket("udp4", net.JoinHostPort("", itoa(port))); err == nil {
		plain.Close()
		t.Fatalf("a non-sharing listener must not get port %d while sharing listeners hold it", port)
	}
}

func TestShareSocketReportsControlAndOptionErrors(t *testing.T) {
	boom := errors.New("control failed")
	if err := shareSocket("udp4", ":0", fakeRawConn{controlErr: boom}); !errors.Is(err, boom) {
		t.Fatalf("a failing Control must be reported, got %v", err)
	}
	if err := shareSocket("udp4", ":0", fakeRawConn{fd: ^uintptr(0)}); err == nil {
		t.Fatal("setting options on an invalid descriptor must be reported")
	}
}
