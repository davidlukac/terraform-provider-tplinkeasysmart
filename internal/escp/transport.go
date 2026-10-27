package escp

import (
	"context"
	"fmt"
	"net"
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
	if got := MAC(h.SwitchMAC); !c.cfg.MAC.IsZero() && !got.IsZero() && got != c.cfg.MAC {
		// the address answers, but it is another device: remember it and let exchange say so
		c.wrong = &WrongSwitchError{Want: c.cfg.MAC, Got: got, IP: addrOf(from.IP)}
		return reply{}, false
	}
	if !c.haveSwMAC {
		c.swMAC, c.haveSwMAC = h.SwitchMAC, true
	}
	return reply{op: h.Op, err: h.ErrCode, tlvs: t, fragment: h.Fragment}, true
}

// recv reads packets until one is an acceptable reply to the current request
// or the deadline passes.
func (c *Client) recv(ctx context.Context, deadline time.Time) (reply, bool) {
	buf := make([]byte, maxDatagram)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		_ = c.conn.SetReadDeadline(deadline)
		n, from, err := c.conn.ReadFromUDP(buf)
		if err != nil {
			return reply{}, false
		}
		if rep, ok := c.accept(from, buf[:n]); ok {
			return rep, true
		}
		if c.wrong != nil {
			return reply{}, false // another device answers: waiting longer or resending will not change that
		}
	}
	return reply{}, false
}

// interruptOnDone makes a read that is waiting for a reply return when ctx is done, instead of
// after the attempt's timeout. The returned function undoes it; call it when the exchange ends.
// Callers hold c.mu and have opened the socket.
//
// The interrupt runs in its own goroutine and may start after the exchange has ended, when the
// next call is already waiting with a deadline of its own: it must not cut that one short. The
// returned function therefore waits for a running interrupt and disarms a late one.
func (c *Client) interruptOnDone(ctx context.Context) func() {
	conn := c.conn // the function may run after Close has dropped the field
	var mu sync.Mutex
	finished := false
	stop := context.AfterFunc(ctx, func() {
		mu.Lock()
		defer mu.Unlock()
		if !finished {
			_ = conn.SetReadDeadline(time.Now())
		}
	})
	return func() {
		stop()
		mu.Lock()
		finished = true
		mu.Unlock()
	}
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
	defer c.interruptOnDone(ctx)()
	c.wrong = nil
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if err := expired(ctx); err != nil { // a cancelled call sends nothing, not even a retry
			return reply{}, err
		}
		if err := c.send(op, tlvs, static, to); err != nil {
			return reply{}, err
		}
		if rep, ok := c.recv(ctx, c.attemptDeadline(ctx)); ok {
			return rep, nil
		}
		if c.wrong != nil {
			return reply{}, c.wrong
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
	defer c.interruptOnDone(ctx)()
	for attempt := 0; attempt <= c.cfg.Retries; attempt++ {
		if err := expired(ctx); err != nil {
			return nil, err
		}
		if err := c.send(op, tlvs, false, []*net.UDPAddr{c.dest}); err != nil {
			return nil, err
		}
		var out []byte
		for {
			rep, ok := c.recv(ctx, c.attemptDeadline(ctx))
			if !ok {
				break
			}
			if rep.err != 0 {
				return nil, &RejectedError{Code: rep.err}
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
	return []*net.UDPAddr{c.dest, {IP: net.IP(c.cfg.BroadcastAddr.AsSlice()), Port: c.cfg.SwitchPort}}
}
