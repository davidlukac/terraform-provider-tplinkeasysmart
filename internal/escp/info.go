package escp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

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
	if c.dest.IP == nil {
		if err := c.relocate(ctx); err != nil {
			return Info{}, err
		}
	}
	if c.cfg.MAC.IsZero() || c.haveSwMAC {
		return c.discoverOnce(ctx)
	}
	// First contact with a switch that is pinned by MAC: the address gets a short budget (a switch
	// answers a discovery within half a second), because a stale address would otherwise cost
	// every attempt of the normal timeout before the lookup by MAC starts.
	quick, cancel := context.WithTimeout(ctx, firstContactBudget)
	info, err := c.discoverOnce(quick)
	cancel()
	if err != nil && ctx.Err() == nil && (errors.Is(err, ErrNoReply) || errors.Is(err, context.DeadlineExceeded)) {
		// the configured address does not answer: the switch may have another one now
		if rerr := c.relocate(ctx); rerr != nil {
			return Info{}, rerr
		}
		info, err = c.discoverOnce(ctx)
	}
	return info, err
}

// firstContactBudget is how long the configured address gets to answer the first discovery of a
// client that pins the switch by MAC before the switch is looked up by it.
const firstContactBudget = 1500 * time.Millisecond

// discoverOnce is one discovery exchange with the switch's current address. A reply whose MAC is not
// Config.MAC is refused, whichever way the MAC was carried.
func (c *Client) discoverOnce(ctx context.Context) (Info, error) {
	r, err := c.exchange(ctx, wire.OpDiscovery, nil, true, c.discoverTargets())
	if err != nil {
		return Info{}, err
	}
	info := infoFromReply(r)
	if !c.cfg.MAC.IsZero() && !info.MAC.IsZero() && info.MAC != c.cfg.MAC {
		return Info{}, &WrongSwitchError{Want: c.cfg.MAC, Got: info.MAC, IP: c.dest.IP}
	}
	return info, nil
}

// relocate finds the switch by Config.MAC and points the client at the address it answers from.
// Callers hold c.mu. The destination is changed in place, because requests in flight hold it.
func (c *Client) relocate(ctx context.Context) error {
	found, err := c.locator(ctx, c.cfg.MAC)
	if errors.Is(err, ErrNotFound) && ctx.Err() == nil {
		// Once in a while a lookup finds nobody although the switch is there: seen right after a
		// management move, when the first lookup missed and the next one, half a second later, found it.
		// Failing a whole run for that costs more than the second window of a lookup that was going to fail.
		found, err = c.locator(ctx, c.cfg.MAC)
	}
	if err != nil {
		return err
	}
	return c.follow(found)
}

// infoFromReply maps the TLVs of a discovery reply to Info; absent or
// malformed fields stay empty.
func infoFromReply(r reply) Info { return infoFromTLVs(r.tlvs) }

// infoFromTLVs is infoFromReply for a bare TLV list, which Discover also has.
func infoFromTLVs(tlvs []wire.TLV) Info {
	r := reply{tlvs: tlvs}
	str := func(t int16) string { v, _ := r.tlv(t); return wire.CString(v) }
	ip := func(t int16) (a netip.Addr) {
		if v, ok := r.tlv(t); ok && len(v) == net.IPv4len {
			a, _ = netip.AddrFromSlice(v)
		}
		return a
	}
	info := Info{Model: str(wire.TLVModel), Hostname: Hostname(str(wire.TLVHostname)), Firmware: ParseFirmware(str(wire.TLVFirmware)),
		Hardware: str(wire.TLVHardware), IP: ip(wire.TLVIP), Netmask: ip(wire.TLVNetmask), Gateway: ip(wire.TLVGateway)}
	if v, ok := r.tlv(wire.TLVMAC); ok {
		info.MAC, _ = MACFromBytes(v) // a reply with a malformed MAC has none
	}
	if v, ok := r.tlv(wire.TLVDHCP); ok && len(v) == 1 {
		info.DHCP = v[0] != 0
	}
	if v, ok := r.tlv(wire.TLVFactoryDefault); ok && len(v) == 1 {
		info.FactoryDefault = v[0] != 0
	}
	return info
}
