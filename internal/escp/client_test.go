package escp

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

func TestDialAppliesDefaults(t *testing.T) {
	c, err := Dial(Config{Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if c.cfg.Timeout != 3*time.Second || c.cfg.Retries != 2 || c.cfg.SwitchPort != defaultSwitchPort ||
		c.cfg.HostPort != defaultHostPort || c.cfg.BroadcastAddr != "255.255.255.255" {
		t.Fatalf("defaults not applied: %+v", c.cfg)
	}
	if c.conn != nil {
		t.Fatal("Dial must not bind the UDP port; that happens on first use")
	}
	if err := c.Close(); err != nil {
		t.Fatalf("closing a client that never opened a socket must succeed: %v", err)
	}
}

func TestDialKeepsExplicitValues(t *testing.T) {
	want := Config{Host: "127.0.0.1", Timeout: time.Second, Retries: 5, SwitchPort: 1111, HostPort: 2222, BroadcastAddr: "10.0.0.255"}
	c, err := Dial(want)
	if err != nil {
		t.Fatal(err)
	}
	// Config holds a func (the write gate), so it is not comparable with ==; the gate is nil here.
	if !reflect.DeepEqual(c.cfg, want) {
		t.Fatalf("explicit configuration was overwritten: %+v", c.cfg)
	}
}

// Zero selects the default, a negative value selects "no retry"; a positive one is kept.
func TestDialRetriesSemantics(t *testing.T) {
	for _, tc := range []struct{ in, want int }{{0, 2}, {NoRetries, 0}, {-7, 0}, {1, 1}, {5, 5}} {
		c, err := Dial(Config{Host: "127.0.0.1", Retries: tc.in})
		if err != nil {
			t.Fatal(err)
		}
		if c.cfg.Retries != tc.want {
			t.Errorf("Retries %d became %d, want %d", tc.in, c.cfg.Retries, tc.want)
		}
	}
}

// With NoRetries the first lost packet is the answer; with one retry the same loss is absorbed.
func TestNoRetriesSendsEachRequestOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		retries int
		wantErr error
	}{{"no retries", NoRetries, ErrNoReply}, {"one retry", 1, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			f.DropFirst(2) // both datagrams of the first attempt are lost
			c := newBoundClient(t, Config{Host: "127.0.0.1", Timeout: 80 * time.Millisecond, Retries: tc.retries,
				SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1"})
			c.allowLoopback = true
			t.Cleanup(func() { c.Close() })
			_, err := c.Info(context.Background())
			if !errors.Is(err, tc.wantErr) { // errors.Is(nil, nil) is true
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestDialRejectsHostThatIsNotIPv4(t *testing.T) {
	_, err := Dial(Config{Host: "::1"})
	if err == nil || !strings.Contains(err.Error(), `resolving "::1"`) {
		t.Fatalf("want a resolution error naming the host, got %v", err)
	}
}

func TestCloseReleasesTheSocket(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.conn == nil {
		t.Fatal("the first request must open the socket")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if c.conn != nil {
		t.Fatal("Close must forget the socket")
	}
	// the client is reusable: the next request binds again
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatalf("reuse after Close: %v", err)
	}
}

func TestBusyHostPortIsReportedOnFirstUse(t *testing.T) {
	f := escptest.Start(t)
	busy, err := net.ListenUDP("udp4", &net.UDPAddr{Port: escptest.FreeUDPPort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.LocalAddr().(*net.UDPAddr).Port
	c, err := Dial(Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 80 * time.Millisecond,
		SwitchPort: f.Port(), HostPort: port, BroadcastAddr: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"Info":   func() error { _, err := c.Info(context.Background()); return err },
		"LED":    func() error { _, err := c.LED(context.Background()); return err },
		"SetLED": func() error { return c.SetLED(context.Background(), true) },
	} {
		err := call()
		if err == nil || !strings.Contains(err.Error(), "binding UDP port") {
			t.Errorf("%s: want a bind error mentioning the port, got %v", name, err)
		}
	}
}

func TestLocalMAC(t *testing.T) {
	t.Run("loopback has no hardware address", func(t *testing.T) {
		if got := localMAC(net.IPv4(127, 0, 0, 1)); got != [6]byte{} {
			t.Fatalf("got %x, want zero", got)
		}
	})
	t.Run("destination that cannot be dialled", func(t *testing.T) {
		if got := localMAC(net.ParseIP("::1")); got != [6]byte{} {
			t.Fatalf("got %x, want zero", got)
		}
	})
	t.Run("interface with a hardware address", func(t *testing.T) {
		ifs, _ := net.Interfaces()
		for _, ifc := range ifs {
			if len(ifc.HardwareAddr) != 6 {
				continue
			}
			addrs, _ := ifc.Addrs()
			for _, a := range addrs {
				ipn, ok := a.(*net.IPNet)
				if !ok || ipn.IP.To4() == nil || ipn.IP.IsLoopback() {
					continue
				}
				var want [6]byte
				copy(want[:], ifc.HardwareAddr)
				if got := localMAC(ipn.IP); got != want {
					t.Fatalf("localMAC(%s) = %x, want %x of %s", ipn.IP, got, want, ifc.Name)
				}
				return
			}
		}
		t.Skip("no non-loopback IPv4 interface with a hardware address on this host")
	})
}

// Several clients (several provider instances, one per switch) must be able to
// hold the reply port at the same time.
func TestSeveralClientsShareTheReplyPort(t *testing.T) {
	port := escptest.FreeUDPPort(t)
	var clients []*Client
	for i := 0; i < 3; i++ {
		c, err := Dial(Config{Host: "127.0.0.1", HostPort: port})
		if err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		err = c.open()
		c.mu.Unlock()
		if err != nil {
			t.Fatalf("client %d could not share port %d: %v", i+1, port, err)
		}
		clients = append(clients, c)
	}
	for _, c := range clients {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
