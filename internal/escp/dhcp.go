package escp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// leaseWait is how long EnableDHCP waits for the switch to report a lease. Switch 03 took about four
// seconds, two of them silent; a slow DHCP server needs more.
const leaseWait = 60 * time.Second

// EnableDHCP makes the switch lease its management address from a DHCP server. It does nothing when
// the switch already leases one.
//
// DANGER: this moves the switch to an address the server chooses, which this client cannot know in
// advance, so the call needs Config.MAC (otherwise ErrInvalid): the client looks the switch up by it
// until discovery reports the DHCP flag on with a usable address, then follows it, so later calls on
// this client go to the leased address. The switch is silent for a moment while it asks for the
// lease (measured: about two seconds), and it ignores a static address written while it leases
// one, so SetIPConfig is the way back.
//
// The write may be applied without a reply. When it got none and the lookup finds the switch static
// at its old address, the error says the write was not applied (it matches ErrNoReply); when nothing
// can be established the error matches ErrMoveUnconfirmed and the caller must look for the switch by its
// MAC before it believes anything. Callers must guard: a network without a DHCP server leaves the
// switch without an address, and the way back then needs this host to be on the address the switch
// falls back to.
func (c *Client) EnableDHCP(ctx context.Context) error {
	if c.cfg.MAC.IsZero() {
		return invalidf("escp: EnableDHCP needs Config.MAC: the leased address is not known in advance, and the switch can only be found again by its MAC")
	}
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	if info.DHCP {
		return nil
	}
	before, err := ipConfigFromInfo(info)
	if err != nil {
		return err
	}
	sent, err := c.writeSystem(ctx, Operation{Name: "EnableDHCP", Impact: ImpactManagement}, info.Hostname, true, before)
	var refused *RejectedError
	if err != nil && (!sent || errors.As(err, &refused)) {
		return err // nothing was applied: the gate or the switch refused, or the request was not sent
	}
	// Applied, or possibly applied: the answer is what discovery shows, even if ctx is done.
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), settleBudget)
		defer cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.awaitLease(ctx, before, err)
}

// awaitLease polls for the switch by MAC until it reports DHCP on with a usable address, then points
// the client at that address. lost is the error of the write when it got no reply. Callers hold c.mu.
func (c *Client) awaitLease(ctx context.Context, before IPConfig, lost error) error {
	wait := c.moveWait
	if wait == 0 {
		wait = leaseWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var last Info
	seen := false
	for {
		if found, err := c.locator(ctx, c.cfg.MAC); err == nil {
			last, seen = found, true
			if _, perr := ipConfigFromInfo(found); found.DHCP && perr == nil {
				return c.follow(found)
			}
		}
		select {
		case <-ctx.Done():
			return leaseNotConfirmed(before, last, seen, lost)
		case <-time.After(ipMovePoll):
		}
	}
}

// leaseNotConfirmed says what is known when no lease was reported in time.
func leaseNotConfirmed(before IPConfig, last Info, seen bool, lost error) error {
	switch {
	case seen && !last.DHCP && lost != nil && errors.Is(lost, ErrNoReply):
		return fmt.Errorf("%w; a lookup shows the switch still static at %s, so DHCP was not switched on", lost, last.IP)
	case seen:
		return fmt.Errorf("%w: no lease was reported; the switch answers at %s with DHCP %s", ErrMoveUnconfirmed, last.IP, onOff(last.DHCP))
	default:
		return fmt.Errorf("%w: no lease was reported and nobody with the configured MAC answers a lookup (the switch was at %s)", ErrMoveUnconfirmed, before.IP)
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

// follow points the client at the address a lookup found the switch at, with a fresh session.
// Callers hold c.mu. The destination is changed in place, because requests in flight hold it.
func (c *Client) follow(found Info) error {
	ip := net.ParseIP(found.IP).To4()
	if ip == nil {
		return fmt.Errorf("%w: %s answered without a usable address", ErrNotFound, c.cfg.MAC)
	}
	c.dest.IP = ip
	c.hostMAC = localMAC(ip)
	c.session = nil
	return nil
}
