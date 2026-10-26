package escp

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/udp"
)

// Client talks ESCP to one switch. It is safe for concurrent use; requests
// are serialized because the switch answers one request at a time. Use one Client per
// switch: two Clients for the same switch were measured to fail completely, apparently
// because the switch keeps a single session (see the package documentation).
type Client struct {
	cfg  Config
	mu   sync.Mutex
	conn *net.UDPConn
	dest *net.UDPAddr
	// alsoFrom is a second address replies are accepted from while the switch is
	// being moved to it (SetIPConfig); nil otherwise.
	alsoFrom net.IP
	// moveWait overrides ipMoveWait (tests); zero means the default.
	moveWait time.Duration
	// allowLoopback lets tests move a fake switch on 127.0.0.0/8 (tests only).
	allowLoopback bool

	hostMAC, swMAC [6]byte
	haveSwMAC      bool
	seq            uint16
	session        []byte
	// switchPorts is the switch's number of ports, read on the first write that names a port
	// (zero until then) and kept: it is hardware. Guarded by mu.
	switchPorts int
	// wrong is the device that answered the request in flight with a MAC other than Config.MAC.
	wrong *WrongSwitchError
	// locator finds a switch by MAC address; tests replace it.
	locator func(ctx context.Context, mac MAC) (Info, error)
}

// Dial prepares a client for the switch at cfg.Host, which must be an IPv4 address or a
// name that resolves to one (a name is looked up now), or, when cfg.MAC is set, may be left
// empty to find the switch by its MAC address on the first request. It sends nothing to the switch and
// binds no socket: the local UDP port is bound on first use, so merely configuring a
// provider (e.g. `terraform validate`) has no side effects on the switch. Close the client
// when done.
func Dial(cfg Config) (*Client, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	switch {
	case cfg.Retries == 0:
		cfg.Retries = defaultRetries
	case cfg.Retries < 0:
		cfg.Retries = 0
	}
	if cfg.SwitchPort == 0 {
		cfg.SwitchPort = defaultSwitchPort
	}
	if cfg.HostPort == 0 {
		cfg.HostPort = defaultHostPort
	}
	if cfg.BroadcastAddr == "" {
		cfg.BroadcastAddr = "255.255.255.255"
	}
	c := &Client{cfg: cfg, locator: hostLocator}
	if cfg.Host == "" && !cfg.MAC.IsZero() {
		// no address yet: the first request finds the switch by its MAC address
		c.dest = &net.UDPAddr{Port: cfg.SwitchPort}
	} else {
		ip, err := net.ResolveIPAddr("ip4", cfg.Host)
		if err != nil {
			return nil, fmt.Errorf("escp: resolving %q: %w", cfg.Host, err)
		}
		c.dest = &net.UDPAddr{IP: ip.IP, Port: cfg.SwitchPort}
		c.hostMAC = localMAC(ip.IP)
	}
	var n [2]byte
	_, _ = rand.Read(n[:])
	c.seq = uint16(n[0])<<8 | uint16(n[1])
	c.seq %= 1000
	return c, nil
}

// Close releases the UDP socket, if it was opened, so that another client or Discover can
// use the reply port. A closed client opens the socket again if it is used afterwards.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Client) open() error {
	if c.conn != nil {
		return nil
	}
	lc := net.ListenConfig{Control: udp.Share}
	pc, err := lc.ListenPacket(context.Background(), "udp4", net.JoinHostPort("", strconv.Itoa(c.cfg.HostPort)))
	if err != nil {
		return fmt.Errorf("escp: binding UDP port %d (is a client that does not share the port running, such as an old Easy Smart utility?): %w", c.cfg.HostPort, err)
	}
	c.conn = pc.(*net.UDPConn) // a "udp4" listener is always a *net.UDPConn
	return nil
}

// localMAC returns the MAC of the interface used to reach dst, or zero.
func localMAC(dst net.IP) (mac [6]byte) {
	probe, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: dst, Port: 9})
	if err != nil {
		return
	}
	defer func() { _ = probe.Close() }() // best-effort: the probe socket only reveals the local address
	local := probe.LocalAddr().(*net.UDPAddr).IP
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(local) && len(ifc.HardwareAddr) == 6 {
				copy(mac[:], ifc.HardwareAddr)
				return
			}
		}
	}
	return
}

// hostLocator is the lookup a client uses: Locate on the interfaces of this host.
//
// coverage: ignore - thin wrapper that would probe the real network; Locate is tested through locateWith
func hostLocator(ctx context.Context, mac MAC) (Info, error) {
	return Locate(ctx, mac, DiscoverOptions{})
}
