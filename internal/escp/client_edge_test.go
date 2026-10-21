package escp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
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
	if c.cfg != want {
		t.Fatalf("explicit configuration was overwritten: %+v", c.cfg)
	}
}

func TestDialRejectsHostThatIsNotIPv4(t *testing.T) {
	_, err := Dial(Config{Host: "::1"})
	if err == nil || !strings.Contains(err.Error(), `resolving "::1"`) {
		t.Fatalf("want a resolution error naming the host, got %v", err)
	}
}

func TestCloseReleasesTheSocket(t *testing.T) {
	f := startFakeSwitch(t)
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
	f := startFakeSwitch(t)
	busy, err := net.ListenUDP("udp4", &net.UDPAddr{Port: freeUDPPort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.LocalAddr().(*net.UDPAddr).Port
	c, err := Dial(Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 80 * time.Millisecond,
		SwitchPort: f.port(), HostPort: port, BroadcastAddr: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() error{
		"Info":   func() error { _, err := c.Info(context.Background()); return err },
		"GetLED": func() error { _, err := c.GetLED(context.Background()); return err },
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

func TestReplyTLVLookup(t *testing.T) {
	r := reply{tlvs: []wire.TLV{{Type: 5, Value: []byte{1}}, {Type: 9, Value: []byte{2, 3}}}}
	if v, ok := r.tlv(9); !ok || len(v) != 2 || v[1] != 3 {
		t.Fatalf("existing TLV not found: %v %v", v, ok)
	}
	if v, ok := r.tlv(7); ok || v != nil {
		t.Fatalf("missing TLV must report not found, got %v %v", v, ok)
	}
}

func TestDecryptChoosesTheKeyThatYieldsAHeader(t *testing.T) {
	pkt := wire.Marshal(wire.Header{Op: wire.OpReply, Seq: 4}, nil)
	garbage := []byte("this is definitely not an ESCP packet, and long enough to look like one....")

	noSession := &Client{}
	if got := noSession.decrypt(wire.RC4Static(pkt)); !wire.LooksLikeHeader(got) {
		t.Fatal("a statically encrypted packet must decode without a session")
	}
	if got := noSession.decrypt(garbage); string(got) != string(wire.RC4Static(garbage)) {
		t.Fatal("without a session, undecodable data falls back to the static key")
	}

	key := []byte("AbCdEf12")
	withSession := &Client{session: key}
	if got := withSession.decrypt(wire.RC4Session(pkt, key)); !wire.LooksLikeHeader(got) {
		t.Fatal("a session-encrypted packet must decode with the session key")
	}
	if got := withSession.decrypt(wire.RC4Static(pkt)); !wire.LooksLikeHeader(got) {
		t.Fatal("a statically encrypted packet must still decode while a session exists")
	}
}

func TestAcceptFiltersForeignAndStaleDatagrams(t *testing.T) {
	c, err := Dial(Config{Host: "127.0.0.1", SwitchPort: 1})
	if err != nil {
		t.Fatal(err)
	}
	c.seq = 41
	fromSwitch := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	enc := func(op byte, seq uint16, tlvs ...wire.TLV) []byte {
		return wire.RC4Static(wire.Marshal(wire.Header{Op: op, Seq: seq, ErrCode: 3, SwitchMAC: [6]byte{1, 2, 3, 4, 5, 6}}, tlvs))
	}
	tests := []struct {
		name string
		from *net.UDPAddr
		data []byte
		ok   bool
	}{
		{"other source address", &net.UDPAddr{IP: net.IPv4(10, 9, 8, 7)}, enc(wire.OpReply, 41), false},
		{"not a packet", fromSwitch, []byte("garbage"), false},
		{"stale sequence number", fromSwitch, enc(wire.OpReply, 40), false},
		{"our own discovery echoed back", fromSwitch, enc(wire.OpDiscovery, 41), false},
		{"the awaited reply", fromSwitch, enc(wire.OpReply, 41, wire.TLV{Type: 9, Value: []byte{7}}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rep, ok := c.accept(tt.from, tt.data)
			if ok != tt.ok {
				t.Fatalf("accept = %v, want %v", ok, tt.ok)
			}
			if ok && (rep.op != wire.OpReply || rep.err != 3 || len(rep.tlvs) != 1 || rep.tlvs[0].Value[0] != 7) {
				t.Fatalf("decoded reply wrong: %+v", rep)
			}
		})
	}
	if !c.haveSwMAC || c.swMAC != [6]byte{1, 2, 3, 4, 5, 6} {
		t.Fatalf("the switch MAC must be learned from the first accepted reply, got %x (have=%v)", c.swMAC, c.haveSwMAC)
	}
	// a later reply with another MAC must not overwrite what we learned
	c.accept(fromSwitch, wire.RC4Static(wire.Marshal(wire.Header{Op: wire.OpReply, Seq: 41, SwitchMAC: [6]byte{9, 9, 9, 9, 9, 9}}, nil)))
	if c.swMAC != [6]byte{1, 2, 3, 4, 5, 6} {
		t.Fatalf("learned MAC was overwritten: %x", c.swMAC)
	}
}

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
	want := Info{Model: "TL-SG108E", Netmask: "255.255.255.0"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSendFailureIsReported(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if err := c.open(); err != nil {
		t.Fatal(err)
	}
	c.conn.Close() // sabotage the socket behind the client's back
	_, err := c.Info(context.Background())
	if err == nil || !strings.Contains(err.Error(), "escp: send") {
		t.Fatalf("want a send error, got %v", err)
	}
}

func TestContextDeadlineBeatsTheConfiguredTimeout(t *testing.T) {
	f := startFakeSwitch(t)
	f.goMute()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Timeout: 5 * time.Second, SwitchPort: f.port(), BroadcastAddr: "127.0.0.1"})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Info(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the 150ms context deadline was ignored, request took %v", elapsed)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	f := startFakeSwitch(t)
	f.goMute()
	c := dialFast(t, f, "secret")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Info(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestSessionSetupFailures(t *testing.T) {
	t.Run("switch does not answer discovery", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.goMute()
		c := dialFast(t, f, "secret")
		if _, err := c.GetLED(context.Background()); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
	})
	t.Run("switch does not answer the key exchange", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.configure(func(f *fakeSwitch) { f.rsaDrop = true })
		c := dialFast(t, f, "secret")
		err := c.SetLED(context.Background(), true)
		if !errors.Is(err, ErrNoReply) || !strings.Contains(err.Error(), "session key exchange") {
			t.Fatalf("want a wrapped ErrNoReply from the key exchange, got %v", err)
		}
		if c.session != nil {
			t.Fatal("a failed key exchange must not leave a session behind")
		}
	})
	t.Run("switch refuses the key", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.configure(func(f *fakeSwitch) { f.rsaCode = 77 })
		c := dialFast(t, f, "secret")
		err := c.SetLED(context.Background(), true)
		if err == nil || !strings.Contains(err.Error(), "unexpected error code 77") {
			t.Fatalf("want the refusal code in the error, got %v", err)
		}
		if c.session != nil {
			t.Fatal("a refused key must not leave a session behind")
		}
		// the next call tries again and succeeds once the switch cooperates
		f.configure(func(f *fakeSwitch) { f.rsaCode = 0 })
		if err := c.SetLED(context.Background(), true); err != nil {
			t.Fatalf("retry after the switch recovered: %v", err)
		}
	})
	t.Run("entropy source fails", func(t *testing.T) {
		f := startFakeSwitch(t)
		c := dialFast(t, f, "secret")
		orig := randReader
		randReader = failingReader{}
		defer func() { randReader = orig }()
		if err := c.SetLED(context.Background(), true); err == nil || !strings.Contains(err.Error(), "entropy unavailable") {
			t.Fatalf("want the entropy error, got %v", err)
		}
		if c.session != nil {
			t.Fatal("no session may exist without a key")
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }

func TestAuthenticatedRequestFailures(t *testing.T) {
	t.Run("SET gets no answer", func(t *testing.T) {
		f := startFakeSwitch(t)
		c := dialFast(t, f, "secret")
		if _, err := c.GetLED(context.Background()); err != nil { // establish the session first
			t.Fatal(err)
		}
		f.configure(func(f *fakeSwitch) { f.dropSets = true })
		if err := c.SetLED(context.Background(), false); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
		if led, _, sets := f.state(); !led || sets != 0 {
			t.Fatal("an unanswered SET must not have been applied by the fake")
		}
	})
	t.Run("GET gets no answer", func(t *testing.T) {
		f := startFakeSwitch(t)
		c := dialFast(t, f, "secret")
		f.configure(func(f *fakeSwitch) {
			f.getFn = func([]int16) ([]wire.TLV, int32, bool) { return nil, 0, true }
		})
		if _, err := c.GetLED(context.Background()); !errors.Is(err, ErrNoReply) {
			t.Fatalf("want ErrNoReply, got %v", err)
		}
	})
	t.Run("LED reply without the LED TLV", func(t *testing.T) {
		f := startFakeSwitch(t)
		c := dialFast(t, f, "secret")
		f.configure(func(f *fakeSwitch) {
			f.getFn = func([]int16) ([]wire.TLV, int32, bool) { return []wire.TLV{{Type: 99, Value: []byte{1}}}, 0, false }
		})
		if _, err := c.GetLED(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected LED reply") {
			t.Fatalf("want an unexpected-reply error, got %v", err)
		}
	})
	t.Run("LED reply with the wrong length", func(t *testing.T) {
		f := startFakeSwitch(t)
		c := dialFast(t, f, "secret")
		f.configure(func(f *fakeSwitch) {
			f.getFn = func([]int16) ([]wire.TLV, int32, bool) {
				return []wire.TLV{{Type: wire.TLVLED, Value: []byte{1, 1}}}, 0, false
			}
		})
		if _, err := c.GetLED(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected LED reply") {
			t.Fatalf("want an unexpected-reply error, got %v", err)
		}
	})
}

func TestWrongPasswordIsRejectedForEverySetter(t *testing.T) {
	setters := map[string]func(*Client, context.Context) error{
		"SetLED":            func(c *Client, ctx context.Context) error { return c.SetLED(ctx, true) },
		"SetIGMP":           func(c *Client, ctx context.Context) error { return c.SetIGMP(ctx, true, false) },
		"SetLoopPrevention": func(c *Client, ctx context.Context) error { return c.SetLoopPrevention(ctx, true) },
		"SetPort":           func(c *Client, ctx context.Context) error { return c.SetPort(ctx, 7, true, "auto", false) },
		"SetQoSMode":        func(c *Client, ctx context.Context) error { return c.SetQoSMode(ctx, 0) },
		"SetBandwidth":      func(c *Client, ctx context.Context) error { return c.SetBandwidth(ctx, 7, 64, 64) },
		"SetStormControl":   func(c *Client, ctx context.Context) error { return c.SetStormControl(ctx, 7, true, false, false, 64) },
		"SetPortMirror": func(c *Client, ctx context.Context) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 7, IngressPorts: []int{4}})
		},
		"SetLAG":           func(c *Client, ctx context.Context) error { return c.SetLAG(ctx, 2, []int{6, 7}) },
		"SetPortBasedVLAN": func(c *Client, ctx context.Context) error { return c.SetPortBasedVLAN(ctx, 2, []int{6, 7}) },
	}
	for name, call := range setters {
		t.Run(name, func(t *testing.T) {
			f := startFakeSwitch(t)
			c := dialFast(t, f, "wrong-password")
			if err := call(c, context.Background()); !errors.Is(err, ErrAuth) {
				t.Fatalf("want ErrAuth, got %v", err)
			}
			if got := f.allSets(); len(got) != 0 {
				t.Fatalf("a rejected SET must not be applied, fake recorded %d", len(got))
			}
		})
	}
}
