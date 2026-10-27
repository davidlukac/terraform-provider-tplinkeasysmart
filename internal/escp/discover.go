package escp

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/udp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// defaultDiscoverWindow is how long Discover listens when DiscoverOptions.Window is zero.
// Switches answer within milliseconds; the rest is room for a busy LAN or a bridge.
const defaultDiscoverWindow = 2 * time.Second

// ErrNoInterface means there is no network interface to send a discovery probe on: none
// is up with an IPv4 broadcast address, or the one asked for is not among them.
var ErrNoInterface = errors.New("escp: no network interface to discover on")

// DiscoverOptions tunes Discover. The zero value probes every usable interface and listens
// for two seconds.
type DiscoverOptions struct {
	// Interface restricts discovery to the interface of that name (for example "en0").
	// Empty means every interface that is up, can broadcast, has a MAC address and an IPv4
	// address with a subnet broadcast address.
	Interface string
	// Window is how long to listen for replies; zero means two seconds.
	Window time.Duration
}

// Discover broadcasts an unauthenticated discovery probe and returns every switch that
// answers within the window: one Info per MAC address, sorted by IP address, empty (not an
// error) when nobody answers. It is read-only, needs no credentials and is not subject to a
// WriteGate.
//
// The probe goes to the subnet broadcast address of each interface, with that interface's
// MAC as the sender, so switches behind a second network card are found too. It is sent
// again halfway through the window, because UDP is lossy; duplicate replies are dropped.
// If the context ends early, Discover returns the replies collected so far together with the
// context's error. A failure to bind the socket, or to send on every interface, is an error.
//
// Switches always reply to UDP port 29809 whatever the sender's port. The port is shared
// (see Client), so Discover can run while a Client of the same host is working: on macOS it
// found both switches three times out of three while a Client was reading at the same
// time. Where the system offers no port sharing, or another program holds the port
// exclusively (an old Easy Smart utility), binding fails and Discover returns that error.
func Discover(ctx context.Context, opts DiscoverOptions) ([]Info, error) {
	return discoverWith(ctx, opts, discoverEnv{interfaces: hostInterfaces, switchPort: defaultSwitchPort, hostPort: defaultHostPort})
}

// discoverEnv is everything Discover takes from the host, so tests can aim it at loopback
// and at made-up interfaces.
type discoverEnv struct {
	interfaces func() ([]netInterface, error)
	switchPort int // UDP port the probes go to
	hostPort   int // UDP port the replies come back to, and so the one we listen on
	// write sends one datagram; nil means the socket's WriteToUDP.
	write func(conn *net.UDPConn, pkt []byte, to *net.UDPAddr) error
}

// netInterface is an interface that discovery can use: what is needed to build a probe.
type netInterface struct {
	Name       string
	MAC        MAC
	Broadcasts []netip.Addr // subnet broadcast address of each IPv4 address
}

// usableInterfaces keeps the interfaces a probe can leave through. addrs lists the addresses
// of an interface; an interface whose addresses cannot be read is skipped.
func usableInterfaces(all []net.Interface, addrs func(net.Interface) ([]net.Addr, error)) []netInterface {
	var out []netInterface
	for _, ifc := range all {
		const need = net.FlagUp | net.FlagBroadcast
		if ifc.Flags&need != need || ifc.Flags&net.FlagLoopback != 0 || len(ifc.HardwareAddr) != macLen {
			continue
		}
		list, err := addrs(ifc)
		if err != nil {
			continue
		}
		u := netInterface{Name: ifc.Name}
		copy(u.MAC[:], ifc.HardwareAddr)
		for _, a := range list {
			if b := subnetBroadcast(a); b.IsValid() {
				u.Broadcasts = append(u.Broadcasts, b)
			}
		}
		if len(u.Broadcasts) > 0 {
			out = append(out, u)
		}
	}
	return out
}

// subnetBroadcast returns the broadcast address of an IPv4 network address, or the invalid Addr for
// anything else (IPv6, and /31 or /32 networks, which have no broadcast address).
func subnetBroadcast(a net.Addr) netip.Addr {
	n, ok := a.(*net.IPNet)
	if !ok {
		return netip.Addr{}
	}
	ip := n.IP.To4()
	ones, bits := n.Mask.Size()
	if ip == nil || bits != 8*net.IPv4len || ones >= bits-1 {
		return netip.Addr{}
	}
	var b [net.IPv4len]byte
	for i := range b {
		b[i] = ip[i] | ^n.Mask[len(n.Mask)-net.IPv4len+i]
	}
	return netip.AddrFrom4(b)
}

// hostInterfaces lists the usable interfaces of this host.
//
// coverage: ignore - thin wrapper over net.Interfaces; the filtering is tested in usableInterfaces
func hostInterfaces() ([]netInterface, error) {
	all, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("escp: listing network interfaces: %w", err)
	}
	return usableInterfaces(all, func(i net.Interface) ([]net.Addr, error) { return i.Addrs() }), nil
}

// selectInterfaces applies DiscoverOptions.Interface to the usable interfaces.
func selectInterfaces(all []netInterface, name string) ([]netInterface, error) {
	if name != "" {
		all = slices.DeleteFunc(slices.Clone(all), func(i netInterface) bool { return i.Name != name })
	}
	if len(all) == 0 {
		if name != "" {
			return nil, fmt.Errorf("%w: %q is not up with an IPv4 broadcast address", ErrNoInterface, name)
		}
		return nil, ErrNoInterface
	}
	return all, nil
}

// probe is one discovery request: where it goes and which host MAC it carries.
type probe struct {
	to  *net.UDPAddr
	mac MAC
}

func discoverWith(ctx context.Context, opts DiscoverOptions, env discoverEnv) ([]Info, error) {
	return discoverUntil(ctx, opts, env, nil)
}

// Locate finds the switch with the given MAC address: a discovery probe like Discover's that
// stops at the first reply from that switch, so it normally returns in well under a second (a
// switch needs 250 to 500 milliseconds to answer). It returns ErrNotFound when nobody with that
// MAC answers within the window (two seconds unless opts.Window says otherwise). Read-only and
// not subject to a WriteGate.
func Locate(ctx context.Context, mac MAC, opts DiscoverOptions) (Info, error) {
	return locateWith(ctx, mac, opts, discoverEnv{interfaces: hostInterfaces, switchPort: defaultSwitchPort, hostPort: defaultHostPort})
}

func locateWith(ctx context.Context, mac MAC, opts DiscoverOptions, env discoverEnv) (Info, error) {
	if mac.IsZero() {
		return Info{}, invalidf("escp: Locate needs the MAC address of the switch")
	}
	want := mac.String()
	infos, err := discoverUntil(ctx, opts, env, func(found map[string]Info) bool { _, ok := found[want]; return ok })
	for _, info := range infos {
		if info.MAC == mac {
			return info, nil
		}
	}
	if err != nil {
		return Info{}, err
	}
	return Info{}, fmt.Errorf("%w: %s", ErrNotFound, want)
}

// discoverUntil is discoverWith with an optional condition (done) that ends the search early, checked after each reply.
func discoverUntil(ctx context.Context, opts DiscoverOptions, env discoverEnv, done func(map[string]Info) bool) ([]Info, error) {
	if opts.Window < 0 {
		return nil, invalidf("escp: negative discovery window %v", opts.Window)
	}
	window := opts.Window
	if window == 0 {
		window = defaultDiscoverWindow
	}
	all, err := env.interfaces()
	if err != nil {
		return nil, err
	}
	chosen, err := selectInterfaces(all, opts.Interface)
	if err != nil {
		return nil, err
	}
	var probes []probe
	for _, ifc := range chosen {
		for _, b := range ifc.Broadcasts {
			probes = append(probes, probe{to: &net.UDPAddr{IP: net.IP(b.AsSlice()), Port: env.switchPort}, mac: ifc.MAC})
		}
	}
	return collectReplies(ctx, env, probes, window, done)
}

// collectReplies sends the probes, listens for the window and returns the distinct replies.
func collectReplies(ctx context.Context, env discoverEnv, probes []probe, window time.Duration, done func(map[string]Info) bool) ([]Info, error) {
	lc := net.ListenConfig{Control: udp.Share}
	pc, err := lc.ListenPacket(ctx, "udp4", net.JoinHostPort("", strconv.Itoa(env.hostPort)))
	if err != nil {
		return nil, fmt.Errorf("escp: binding UDP port %d (is a client that does not share the port running, such as an old Easy Smart utility?): %w", env.hostPort, err)
	}
	conn := pc.(*net.UDPConn) // a "udp4" listener is always a *net.UDPConn
	defer func() { _ = conn.Close() }()
	// A cancelled context must end a read that is waiting for its deadline.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	var n [2]byte
	_, _ = rand.Read(n[:])
	seq := (uint16(n[0])<<8 | uint16(n[1])) % 1000

	found := map[string]Info{}
	start := time.Now()
	for _, until := range []time.Time{start.Add(window / 2), start.Add(window)} {
		if err := sendProbes(conn, env, probes, seq); err != nil {
			return nil, err
		}
		listen(ctx, conn, until, seq, found, done)
		if err := ctx.Err(); err != nil {
			return sortedInfos(found), err
		}
		if done != nil && done(found) {
			break
		}
	}
	return sortedInfos(found), nil
}

// sendProbes sends every probe; it fails only when none could be sent, so one interface that
// refuses does not hide the switches on the others.
func sendProbes(conn *net.UDPConn, env discoverEnv, probes []probe, seq uint16) error {
	var lastErr error
	sent := 0
	for _, p := range probes {
		pkt := wire.RC4Static(wire.Marshal(wire.Header{Op: wire.OpDiscovery, HostMAC: p.mac, Seq: seq}, nil))
		var err error
		if env.write != nil {
			err = env.write(conn, pkt, p.to)
		} else {
			_, err = conn.WriteToUDP(pkt, p.to)
		}
		if err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		return fmt.Errorf("escp: sending the discovery probe: %w", lastErr)
	}
	return nil
}

// listen reads datagrams until the deadline and records the discovery replies to seq. Anything
// else (undecodable data, other sequence numbers, echoes of our own probe) is ignored.
func listen(ctx context.Context, conn *net.UDPConn, until time.Time, seq uint16, found map[string]Info, done func(map[string]Info) bool) {
	buf := make([]byte, maxDatagram)
	_ = conn.SetReadDeadline(until)
	// The wake-up that a cancelled context sends to the socket may have fired before the line
	// above and been overwritten by it; looking again afterwards closes that gap.
	if ctx.Err() != nil {
		return
	}
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return // deadline reached, or the context cancelled it
		}
		h, tlvs, err := wire.Unmarshal(wire.RC4Static(buf[:n]))
		if err != nil || h.Op != wire.OpReply || h.Seq != seq {
			continue
		}
		info := infoFromTLVs(tlvs)
		key := info.MAC.String()
		if key == "" {
			key = from.IP.String()
		}
		if _, dup := found[key]; !dup {
			found[key] = info
			if done != nil && done(found) {
				return
			}
		}
	}
}

// sortedInfos returns the replies ordered by IP address (then MAC); an address the reply
// did not carry sorts first.
func sortedInfos(found map[string]Info) []Info {
	out := make([]Info, 0, len(found))
	for _, info := range found {
		out = append(out, info)
	}
	slices.SortFunc(out, func(a, b Info) int {
		if c := a.IP.Compare(b.IP); c != 0 {
			return c
		}
		return bytes.Compare(a.MAC[:], b.MAC[:])
	})
	return out
}
