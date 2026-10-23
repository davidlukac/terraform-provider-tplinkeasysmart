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

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// maxDatagram is the receive buffer size; ESCP packets are well below it.
const maxDatagram = 4096

// maxFragmentedReply bounds a reply reassembled from fragments. The largest real
// one is the configuration file (a few KiB); the cap keeps a misbehaving or
// spoofed sender from growing the buffer until the deadline.
const maxFragmentedReply = 64 << 10

// randReader is the entropy source for session keys; tests replace it to
// provoke the (otherwise unreachable) read failure.
var randReader io.Reader = rand.Reader

// Default ports of the protocol: requests go to 29808, replies always come
// back to 29809 regardless of the source port.
const (
	defaultSwitchPort = 29808
	defaultHostPort   = 29809
)

var (
	// ErrNoReply means the switch did not answer within the timeout.
	ErrNoReply = errors.New("escp: no reply from switch")
	// ErrReplyTooLarge means a fragmented reply grew past the sane limit.
	ErrReplyTooLarge = errors.New("escp: fragmented reply is larger than any switch sends")
	// ErrRejected means the switch rejected a write. Wrong credentials are one reason, but
	// any non-zero reply code lands here (a LAG with ports outside its group's range, for
	// example, is refused with code 4 although the credentials are fine); the
	// *RejectedError that wraps it carries the code.
	ErrRejected = errors.New("escp: switch rejected the request")
	// ErrInvalid means the caller's argument was refused before anything was sent: a port or
	// id out of range, a rate that is not a multiple of the step, a combination the switch
	// cannot keep. The message names the value and what is allowed; test for the class with
	// errors.Is(err, ErrInvalid). It is never returned for a reply the client cannot parse,
	// nor for a failure of the switch.
	ErrInvalid = errors.New("escp: invalid argument")
)

// invalidError is an ErrInvalid with its own message.
type invalidError struct{ msg string }

func (e *invalidError) Error() string { return e.msg }

// Is makes errors.Is(err, ErrInvalid) true for every refused argument.
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

// invalidf builds an error that matches ErrInvalid and reads as the formatted message.
func invalidf(format string, args ...any) error {
	return &invalidError{msg: fmt.Sprintf(format, args...)}
}

// RejectedError is a write the switch answered with a non-zero code. It matches
// ErrRejected (errors.Is) for callers that treat every refusal alike, and names the
// code for those that want to know which one it was. Reason, when set, says why
// the switch refused (only for codes whose cause has been established).
type RejectedError struct {
	Code   int32
	Reason string
}

func (e *RejectedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s (error code %d): %s", ErrRejected.Error(), e.Code, e.Reason)
	}
	return fmt.Sprintf("%s (error code %d): wrong credentials, or the switch refuses this setting or value", ErrRejected.Error(), e.Code)
}

// Is makes errors.Is(err, ErrRejected) true for every rejected write.
func (e *RejectedError) Is(target error) bool { return target == ErrRejected }

// NoRetries is the Config.Retries value that asks for a single attempt per request. (Zero
// cannot mean that: it selects the default.)
const NoRetries = -1

// defaultRetries is the number of additional attempts when Config.Retries is zero.
const defaultRetries = 2

// Config configures a Client. Zero values select sensible defaults, except Host, which is
// required. A Config holds the switch's password: keep it out of logs and error messages.
type Config struct {
	Host string // switch IPv4 address or hostname (required)
	// Username and Password are sent with every write and every authenticated read.
	Username string
	Password string
	Timeout  time.Duration // how long to wait for a reply per attempt, default 3s
	// Retries is the number of additional attempts after the first when the switch stays
	// silent: zero selects the default (2), NoRetries (any negative value) selects none.
	// One request is therefore sent up to Retries+1 times, each waiting up to Timeout. A call
	// that needs a session makes up to four such requests (the key exchange and the request,
	// then both again if the switch stops answering mid-call), plus the discovery that learns
	// the switch's MAC on a client's first call: with the defaults 36 seconds, 45 on the first.
	Retries int

	// SwitchPort and HostPort are the UDP ports of the switch (29808) and of this host
	// (29809). A real switch always replies to 29809 whatever port the request came from,
	// so change them only to talk to a fake switch.
	SwitchPort int
	HostPort   int
	// BroadcastAddr is where discovery probes go besides the switch's own address;
	// default 255.255.255.255.
	BroadcastAddr string

	// WriteGate, when set, is consulted before every write; see WriteGate.
	WriteGate WriteGate
}

// InfoReader is the part of a Client that a WriteGate may use. It only reads.
type InfoReader interface {
	Info(ctx context.Context) (Info, error)
}

// WriteGate decides whether the client may send a write to the switch. Every
// method that changes the switch (each Set* and Delete*) calls it first, with
// the client itself, and sends nothing when it returns an error: the method
// returns that error unchanged. A gate may read from the switch through sw but
// must never write, and must be safe for concurrent use. Reads are never gated.
//
// A method that sends several writes consults the gate before each of them, so
// a gate that is expensive should remember its verdict.
type WriteGate func(ctx context.Context, sw InfoReader) error

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
}

// Dial prepares a client for the switch at cfg.Host, which must be an IPv4 address or a
// name that resolves to one (a name is looked up now). It sends nothing to the switch and
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
	tlvs []wire.TLV
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
		return wire.RC4Static(b)
	}
	return wire.RC4Session(b, c.session)
}

func (c *Client) decrypt(b []byte) []byte {
	if p := wire.RC4Static(b); wire.LooksLikeHeader(p) {
		return p
	}
	if c.session != nil {
		return wire.RC4Session(b, c.session)
	}
	return wire.RC4Static(b)
}

// accept decides whether a datagram is the reply to the request in flight: it
// must come from the switch, decode to a valid packet, echo the current
// sequence number and not be our own broadcast discovery coming back.
func (c *Client) accept(from *net.UDPAddr, data []byte) (reply, bool) {
	if !from.IP.Equal(c.dest.IP) && (c.alsoFrom == nil || !from.IP.Equal(c.alsoFrom)) {
		return reply{}, false
	}
	h, t, err := wire.Unmarshal(c.decrypt(data))
	if err != nil || h.Seq != c.seq || h.Op == wire.OpDiscovery {
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
func (c *Client) send(op byte, tlvs []wire.TLV, static bool, to []*net.UDPAddr) error {
	c.seq = (c.seq + 1) % 1000
	pkt := c.encrypt(wire.Marshal(wire.Header{Op: op, SwitchMAC: c.swMAC, HostMAC: c.hostMAC, Seq: c.seq}, tlvs), static)
	for _, d := range to {
		if _, err := c.conn.WriteToUDP(pkt, d); err != nil {
			return fmt.Errorf("escp: send: %w", err)
		}
	}
	return nil
}

// exchange sends one packet and waits for the reply with the same sequence
// number from the switch, retrying on silence. Callers hold c.mu.
func (c *Client) exchange(ctx context.Context, op byte, tlvs []wire.TLV, static bool, to []*net.UDPAddr) (reply, error) {
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
func (c *Client) exchangeFragments(ctx context.Context, op byte, tlvs []wire.TLV, typ int16) ([]byte, error) {
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
			if len(out) > maxFragmentedReply {
				return nil, ErrReplyTooLarge
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

// Info asks the switch to describe itself with a discovery probe: model, firmware, hardware,
// hostname, MAC and its network settings. It needs no credentials and works on any firmware,
// which is why a WriteGate reads the firmware through it. Each attempt is sent to the
// switch's address and to Config.BroadcastAddr, and repeated up to Config.Retries times; if
// nothing answers the error is ErrNoReply.
func (c *Client) Info(ctx context.Context) (Info, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.discover(ctx)
}

func (c *Client) discover(ctx context.Context) (Info, error) {
	r, err := c.exchange(ctx, wire.OpDiscovery, nil, true, c.discoverTargets())
	if err != nil {
		return Info{}, err
	}
	return infoFromReply(r), nil
}

// infoFromReply maps the TLVs of a discovery reply to Info; absent or
// malformed fields stay empty.
func infoFromReply(r reply) Info { return infoFromTLVs(r.tlvs) }

// infoFromTLVs is infoFromReply for a bare TLV list, which Discover also has.
func infoFromTLVs(tlvs []wire.TLV) Info {
	r := reply{tlvs: tlvs}
	str := func(t int16) string { v, _ := r.tlv(t); return wire.CString(v) }
	ip := func(t int16) string {
		if v, ok := r.tlv(t); ok && len(v) == 4 {
			return net.IP(v).String()
		}
		return ""
	}
	info := Info{Model: str(wire.TLVModel), Hostname: str(wire.TLVHostname), Firmware: str(wire.TLVFirmware),
		Hardware: str(wire.TLVHardware), IP: ip(wire.TLVIP), Netmask: ip(wire.TLVNetmask), Gateway: ip(wire.TLVGateway)}
	if v, ok := r.tlv(wire.TLVMAC); ok && len(v) == 6 {
		info.MAC = net.HardwareAddr(v).String()
	}
	if v, ok := r.tlv(wire.TLVDHCP); ok && len(v) == 1 {
		info.DHCP = v[0] != 0
	}
	if v, ok := r.tlv(wire.TLVFactoryDefault); ok && len(v) == 1 {
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
	key := make([]byte, wire.SessionKeyLen)
	for i := range key {
		n, err := rand.Int(randReader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return err
		}
		key[i] = alphabet[n.Int64()]
	}
	c.session = key // the reply is already encrypted with it
	r, err := c.exchange(ctx, wire.OpSet, []wire.TLV{{Type: wire.TLVRSAKey, Value: wire.RSASessionTLV(key)}}, true, []*net.UDPAddr{c.dest})
	if err != nil {
		c.session = nil
		return fmt.Errorf("session key exchange: %w", err)
	}
	if r.err != wire.ErrCodeKeyAccepted {
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

// LED reports whether the switch LEDs are on.
func (c *Client) LED(ctx context.Context) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var on bool
	err := c.withSession(ctx, func() error {
		r, err := c.exchange(ctx, wire.OpGet, []wire.TLV{{Type: wire.TLVLED, Value: nil}}, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		v, ok := r.tlv(wire.TLVLED)
		if !ok || len(v) != 1 {
			return fmt.Errorf("escp: unexpected LED reply (error code %d, %d TLVs)", r.err, len(r.tlvs))
		}
		on = v[0] == 1
		return nil
	})
	return on, err
}

// SetLED switches the LEDs on or off. It is cosmetic: it does not touch traffic or
// configuration, and it is the one write that is safe to try on any switch.
func (c *Client) SetLED(ctx context.Context, on bool) error {
	return c.setTLVs(ctx, wire.TLV{Type: wire.TLVLED, Value: []byte{boolByte(on)}})
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// setTLVs sends a SET carrying the credentials plus the given settings. It is
// the only place where the client writes, so the write gate sits here: no
// setting can be added without passing it. The gate runs before the lock is
// taken because it reads through the client.
func (c *Client) setTLVs(ctx context.Context, tlvs ...wire.TLV) error {
	if gate := c.cfg.WriteGate; gate != nil {
		if err := gate(ctx, c); err != nil {
			return err
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.withSession(ctx, func() error {
		req := append([]wire.TLV{
			{Type: wire.TLVUsername, Value: append([]byte(c.cfg.Username), 0)},
			{Type: wire.TLVPassword, Value: append([]byte(c.cfg.Password), 0)},
		}, tlvs...)
		r, err := c.exchange(ctx, wire.OpSet, req, false, []*net.UDPAddr{c.dest})
		if err != nil {
			return err
		}
		if r.err != 0 {
			return &RejectedError{Code: r.err}
		}
		return nil
	})
}

// getTLVs reads one or more setting types with an authenticated session and
// returns every TLV of the reply. It never writes.
func (c *Client) getTLVs(ctx context.Context, types ...int16) ([]wire.TLV, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []wire.TLV
	err := c.withSession(ctx, func() error {
		req := make([]wire.TLV, len(types))
		for i, t := range types {
			req[i] = wire.TLV{Type: t}
		}
		r, err := c.exchange(ctx, wire.OpGet, req, false, []*net.UDPAddr{c.dest})
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
