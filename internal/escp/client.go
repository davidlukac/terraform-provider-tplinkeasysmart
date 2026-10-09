package escp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strconv"
	"sync"
	"time"
)

// maxDatagram is the receive buffer size; ESCP packets are well below it.
const maxDatagram = 4096

// randReader is the entropy source for session keys; tests replace it to
// provoke the (otherwise unreachable) read failure.
var randReader io.Reader = rand.Reader

// Default ports of the protocol: requests go to 29808, replies always come
// back to 29809 regardless of the source port.
const (
	DefaultSwitchPort = 29808
	DefaultHostPort   = 29809
)

var (
	// ErrNoReply means the switch did not answer within the timeout.
	ErrNoReply = errors.New("escp: no reply from switch")
	// ErrAuth means the switch rejected the credentials.
	ErrAuth = errors.New("escp: switch rejected the credentials")
)

// Config configures a Client. Zero values select sensible defaults.
type Config struct {
	Host     string // switch IPv4 address or hostname
	Username string
	Password string
	Timeout  time.Duration // per attempt, default 3s
	Retries  int           // additional attempts, default 2

	SwitchPort int // default 29808
	HostPort   int // default 29809
	// BroadcastAddr is only used as discovery fallback, default 255.255.255.255.
	BroadcastAddr string
}

// Info is what a discovery reply reports about the switch.
type Info struct {
	Model, Hostname, MAC, Firmware, Hardware string
	IP, Netmask, Gateway                     string
	DHCP                                     bool
	// FactoryDefault is discovery TLV 14: the switch has never been configured
	// (still on factory credentials).
	FactoryDefault bool
}

// Client talks ESCP to one switch. It is safe for concurrent use; requests
// are serialized because the switch answers one client at a time.
type Client struct {
	cfg  Config
	mu   sync.Mutex
	conn *net.UDPConn
	dest *net.UDPAddr

	hostMAC, swMAC [6]byte
	haveSwMAC      bool
	seq            uint16
	session        []byte
}

// Dial prepares a client. The local UDP port is bound on first use, so merely
// configuring a provider (e.g. `terraform validate`) never touches the network.
func Dial(cfg Config) (*Client, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 3 * time.Second
	}
	if cfg.Retries == 0 {
		cfg.Retries = 2
	}
	if cfg.SwitchPort == 0 {
		cfg.SwitchPort = DefaultSwitchPort
	}
	if cfg.HostPort == 0 {
		cfg.HostPort = DefaultHostPort
	}
	if cfg.BroadcastAddr == "" {
		cfg.BroadcastAddr = "255.255.255.255"
	}
	ip, err := net.ResolveIPAddr("ip4", cfg.Host)
	if err != nil {
		return nil, fmt.Errorf("escp: resolving %q: %w", cfg.Host, err)
	}
	c := &Client{cfg: cfg, dest: &net.UDPAddr{IP: ip.IP, Port: cfg.SwitchPort}}
	c.hostMAC = localMAC(ip.IP)
	var n [2]byte
	_, _ = rand.Read(n[:])
	c.seq = uint16(n[0])<<8 | uint16(n[1])
	c.seq %= 1000
	return c, nil
}

// Close releases the UDP socket, if it was opened.
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
	lc := net.ListenConfig{Control: shareSocket}
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

type reply struct {
	op   byte
	err  int32
	tlvs []TLV
	// fragment is non-zero when more packets of the same reply follow.
	fragment uint16
}

func (r reply) tlv(t int16) ([]byte, bool) {
	for _, x := range r.tlvs {
		if x.Type == t {
			return x.Value, true
		}
	}
	return nil, false
}

func (c *Client) encrypt(b []byte, static bool) []byte {
	if static || c.session == nil {
		return rc4Static(b)
	}
	return rc4Session(b, c.session)
}

func (c *Client) decrypt(b []byte) []byte {
	if p := rc4Static(b); looksLikeHeader(p) {
		return p
	}
	if c.session != nil {
		return rc4Session(b, c.session)
	}
	return rc4Static(b)
}

// accept decides whether a datagram is the reply to the request in flight: it
// must come from the switch, decode to a valid packet, echo the current
// sequence number and not be our own broadcast discovery coming back.
func (c *Client) accept(from *net.UDPAddr, data []byte) (reply, bool) {
	if !from.IP.Equal(c.dest.IP) {
		return reply{}, false
	}
	h, t, err := unmarshal(c.decrypt(data))
	if err != nil || h.Seq != c.seq || h.Op == OpDiscovery {
		return reply{}, false
	}
	if !c.haveSwMAC {
		c.swMAC, c.haveSwMAC = h.SwitchMAC, true
	}
	return reply{op: h.Op, err: h.ErrCode, tlvs: t, fragment: h.Fragment}, true
}

// recv reads packets until one is an acceptable reply to the current request
// or the deadline passes.
func (c *Client) recv(deadline time.Time) (reply, bool) {
	buf := make([]byte, maxDatagram)
	for time.Now().Before(deadline) {
		_ = c.conn.SetReadDeadline(deadline)
		n, from, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			return reply{}, false
		}
		if rep, ok := c.accept(from, buf[:n]); ok {
			return rep, true
		}
	}
	return reply{}, false
}

// expired reports why the call must stop: the context was cancelled, or its
// deadline has passed. The socket deadline equals the context deadline and can
// fire slightly before the context's own timer does, so the deadline is
// compared directly instead of waiting for ctx.Err() to catch up.
func expired(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok && !time.Now().Before(dl) {
		return context.DeadlineExceeded
	}
	return nil
}

// attemptDeadline is the end of the current attempt: the per-attempt timeout,
// capped by the context deadline.
func (c *Client) attemptDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(c.cfg.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		return dl
	}
	return deadline
}

// send transmits one request with a fresh sequence number.
func (c *Client) send(op byte, tlvs []TLV, static bool, to []*net.UDPAddr) error {
	c.seq = (c.seq + 1) % 1000
	pkt := c.encrypt(marshal(header{Op: op, SwitchMAC: c.swMAC, HostMAC: c.hostMAC, Seq: c.seq}, tlvs), static)
	for _, d := range to {
		if _, err := c.conn.WriteToUDP(pkt, d); err != nil {
			return fmt.Errorf("escp: send: %w", err)
		}
	}
	return nil
}

// exchange sends one packet and waits for the reply with the same sequence
// number from the switch, retrying on silence. Callers hold c.mu.
func (c *Client) exchange(ctx context.Context, op byte, tlvs []TLV, static bool, to []*net.UDPAddr) (reply, error) {
	if err := c.open(); err != nil {
		return reply{}, err
	}
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if err := c.send(op, tlvs, static, to); err != nil {
			return reply{}, err
		}
		if rep, ok := c.recv(c.attemptDeadline(ctx)); ok {
			return rep, nil
		}
		if err := expired(ctx); err != nil {
			return reply{}, err
		}
	}
	return reply{}, ErrNoReply
}

// exchangeFragments is exchange for replies that span several packets: it
// concatenates the values of every TLV of the given type until a packet
// without the fragment flag arrives. A missing fragment restarts the request.
func (c *Client) exchangeFragments(ctx context.Context, op byte, tlvs []TLV, typ int16) ([]byte, error) {
	// Callers hold c.mu and run inside withSession, so the socket is open.
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if err := c.send(op, tlvs, false, []*net.UDPAddr{c.dest}); err != nil {
			return nil, err
		}
		var out []byte
		for {
			rep, ok := c.recv(c.attemptDeadline(ctx))
			if !ok {
				break
			}
			if rep.err != 0 {
				return nil, fmt.Errorf("escp: switch answered error code %d", rep.err)
			}
			for _, x := range rep.tlvs {
				if x.Type == typ {
					out = append(out, x.Value...)
				}
			}
			if rep.fragment == 0 {
				return out, nil
			}
		}
		if err := expired(ctx); err != nil {
			return nil, err
		}
	}
	return nil, ErrNoReply
}

func (c *Client) discoverTargets() []*net.UDPAddr {
	return []*net.UDPAddr{c.dest, {IP: net.ParseIP(c.cfg.BroadcastAddr), Port: c.cfg.SwitchPort}}
}

// Info performs an (unauthenticated) discovery and returns the switch details.
func (c *Client) Info(ctx context.Context) (Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.discover(ctx)
}

func (c *Client) discover(ctx context.Context) (Info, error) {
	r, err := c.exchange(ctx, OpDiscovery, nil, true, c.discoverTargets())
	if err != nil {
		return Info{}, err
	}
	return infoFromReply(r), nil
}

// infoFromReply maps the TLVs of a discovery reply to Info; absent or
// malformed fields stay empty.
func infoFromReply(r reply) Info {
	str := func(t int16) string { v, _ := r.tlv(t); return cstring(v) }
	ip := func(t int16) string {
		if v, ok := r.tlv(t); ok && len(v) == 4 {
			return net.IP(v).String()
		}
		return ""
	}
	info := Info{Model: str(TLVModel), Hostname: str(TLVHostname), Firmware: str(TLVFirmware),
		Hardware: str(TLVHardware), IP: ip(TLVIP), Netmask: ip(TLVNetmask), Gateway: ip(TLVGateway)}
	if v, ok := r.tlv(TLVMAC); ok && len(v) == 6 {
		info.MAC = net.HardwareAddr(v).String()
	}
	if v, ok := r.tlv(TLVDHCP); ok && len(v) == 1 {
		info.DHCP = v[0] != 0
	}
	if v, ok := r.tlv(TLVFactoryDefault); ok && len(v) == 1 {
		info.FactoryDefault = v[0] != 0
	}
	return info
}

// ensureSession discovers the switch MAC and performs the RSA session-key
// exchange once; the key is dropped on failures so the next call retries.
func (c *Client) ensureSession(ctx context.Context) error {
	if c.session != nil {
		return nil
	}
	if !c.haveSwMAC {
		if _, err := c.discover(ctx); err != nil {
			return err
		}
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	key := make([]byte, SessionKeyLen)
	for i := range key {
		n, err := rand.Int(randReader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return err
		}
		key[i] = alphabet[n.Int64()]
	}
	c.session = key // the reply is already encrypted with it
	r, err := c.exchange(ctx, OpSet, []TLV{{TLVRSAKey, RSASessionTLV(key)}}, true, []*net.UDPAddr{c.dest})
	if err != nil {
		c.session = nil
		return fmt.Errorf("session key exchange: %w", err)
	}
	if r.err != ErrCodeKeyAccepted {
		c.session = nil
		return fmt.Errorf("session key exchange: unexpected error code %d", r.err)
	}
	return nil
}

// withSession runs fn with an established session, re-establishing it once if
// the switch stops answering (session expired or switch rebooted).
func (c *Client) withSession(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		if err := c.ensureSession(ctx); err != nil {
			return err
		}
		err := fn()
		if errors.Is(err, ErrNoReply) && attempt == 0 {
			c.session = nil
			continue
		}
		return err
	}
}

// GetLED reports whether the switch LEDs are on.
func (c *Client) GetLED(ctx context.Context) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var on bool
	err := c.withSession(ctx, func() error {
		r, err := c.exchange(ctx, OpGet, []TLV{{TLVLED, nil}}, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		v, ok := r.tlv(TLVLED)
		if !ok || len(v) != 1 {
			return fmt.Errorf("escp: unexpected LED reply (error code %d, %d TLVs)", r.err, len(r.tlvs))
		}
		on = v[0] == 1
		return nil
	})
	return on, err
}

// SetLED switches the LEDs on or off. Credentials accompany every SET.
func (c *Client) SetLED(ctx context.Context, on bool) error {
	return c.setTLVs(ctx, TLV{TLVLED, []byte{boolByte(on)}})
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// setTLVs sends a SET carrying the credentials plus the given settings.
func (c *Client) setTLVs(ctx context.Context, tlvs ...TLV) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.withSession(ctx, func() error {
		req := append([]TLV{
			{TLVUsername, append([]byte(c.cfg.Username), 0)},
			{TLVPassword, append([]byte(c.cfg.Password), 0)},
		}, tlvs...)
		r, err := c.exchange(ctx, OpSet, req, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		if r.err != 0 {
			return fmt.Errorf("%w (error code %d)", ErrAuth, r.err)
		}
		return nil
	})
}

// getTLVs reads one or more setting types with an authenticated session and
// returns every TLV of the reply. It never writes.
func (c *Client) getTLVs(ctx context.Context, types ...int16) ([]TLV, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []TLV
	err := c.withSession(ctx, func() error {
		req := make([]TLV, len(types))
		for i, t := range types {
			req[i] = TLV{Type: t}
		}
		r, err := c.exchange(ctx, OpGet, req, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		if r.err != 0 {
			return fmt.Errorf("escp: switch answered error code %d for TLV types %v", r.err, types)
		}
		out = r.tlvs
		return nil
	})
	return out, err
}
