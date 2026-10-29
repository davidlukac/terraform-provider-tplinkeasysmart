package escptest

import (
	"net"
	"testing"
)

// listenAt binds the loopback port the test names, standing in for a kernel that hands a released
// port out again.
func listenAt(t *testing.T, port int) func() (*net.UDPConn, error) {
	t.Helper()
	return func() (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port})
	}
}

func TestAPortHandedOutIsNotGivenAgain(t *testing.T) {
	first := FreeUDPPort(t) // released, but reserved
	calls := 0
	// the kernel offers the port just released first, then a fresh one
	listen := func() (*net.UDPConn, error) {
		calls++
		if calls == 1 {
			return listenAt(t, first)()
		}
		return listenLoopback()
	}
	c, err := listenUnreserved(listen)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }() // best effort at the end of the test
	if got := c.LocalAddr().(*net.UDPAddr).Port; got == first || calls != 2 {
		t.Errorf("got port %d after %d tries, want a port other than %d on the second try", got, calls, first)
	}
}

func TestStartTakesAPortNobodyWasGiven(t *testing.T) {
	given := FreeUDPPort(t)
	sw := Start(t)
	if sw.Port() == given {
		t.Errorf("the fake switch took port %d, which FreeUDPPort had handed out", given)
	}
}

func TestSearchForAPortGivesUpAndSaysSo(t *testing.T) {
	held := FreeUDPPort(t)
	_, err := listenUnreserved(listenAt(t, held))
	if err == nil {
		t.Fatal("expected an error when every offered port was handed out before")
	}
}

func TestListenErrorIsReturned(t *testing.T) {
	blocker, err := listenLoopback()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }() // best effort at the end of the test
	port := blocker.LocalAddr().(*net.UDPAddr).Port
	// binding a port that is in use fails; the error comes back and nothing is reserved for it
	if _, err := listenUnreserved(listenAt(t, port)); err == nil {
		t.Fatal("expected the bind error")
	}
}
