package escp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	// ipMoveWait is how long SetIPConfig waits for the switch to answer at its new address.
	ipMoveWait = 20 * time.Second
	// ipMovePoll is the pause between discovery probes while waiting.
	ipMovePoll = 500 * time.Millisecond
)

// ErrMoveUnconfirmed means the switch was told to move but did not answer at
// the new address in time. Its address is then unknown: look at both.
var ErrMoveUnconfirmed = errors.New("escp: the switch did not answer at its new address")

// IPConfig is the static management address of the switch (System > IP Setting).
type IPConfig struct {
	IP, Netmask, Gateway net.IP
}

// String formats the triple for messages.
func (cfg IPConfig) String() string {
	return fmt.Sprintf("ip %s mask %s gateway %s", cfg.IP, cfg.Netmask, cfg.Gateway)
}

// ipConfigFromInfo parses the address triple of a discovery reply. It refuses
// a reply that does not carry three IPv4 addresses, because anything written
// back from it could cut the management path.
func ipConfigFromInfo(info Info) (IPConfig, error) {
	cfg := IPConfig{IP: net.ParseIP(info.IP).To4(), Netmask: net.ParseIP(info.Netmask).To4(), Gateway: net.ParseIP(info.Gateway).To4()}
	if cfg.IP == nil || cfg.Netmask == nil || cfg.Gateway == nil {
		return IPConfig{}, fmt.Errorf("escp: switch reported an unusable IP configuration (%q %q %q); refusing to write", info.IP, info.Netmask, info.Gateway)
	}
	return cfg, nil
}

// ValidateIPConfig checks a static management address before anything is sent:
// all three are IPv4, the mask is a contiguous prefix between /8 and /30, the
// address is a usable host address of its subnet and the gateway is another
// host of the same subnet (0.0.0.0 means no gateway).
func ValidateIPConfig(cfg IPConfig) error { return validateIPConfig(cfg, false) }

// validateIPConfig is ValidateIPConfig; allowLoopback lets tests move a fake switch that lives on 127.0.0.0/8.
func validateIPConfig(cfg IPConfig, allowLoopback bool) error {
	ip, mask, gw := cfg.IP.To4(), cfg.Netmask.To4(), cfg.Gateway.To4()
	if ip == nil || mask == nil || gw == nil {
		return fmt.Errorf("escp: IP address, subnet mask and gateway must all be IPv4 addresses (%s)", cfg)
	}
	m := net.IPMask(mask)
	ones, bits := m.Size()
	if bits == 0 || ones < 8 || ones > 30 {
		return fmt.Errorf("escp: subnet mask %s is not a contiguous prefix between /8 and /30", mask)
	}
	subnet := net.IPNet{IP: ip.Mask(m), Mask: m}
	if ip.IsUnspecified() || (ip.IsLoopback() && !allowLoopback) || ip.IsMulticast() || ip.Equal(subnet.IP) || ip.Equal(broadcastOf(subnet)) {
		return fmt.Errorf("escp: %s is not a usable host address in %s", ip, subnet.String())
	}
	if gw.IsUnspecified() {
		return nil
	}
	if !subnet.Contains(gw) || gw.Equal(subnet.IP) || gw.Equal(broadcastOf(subnet)) || gw.Equal(ip) {
		return fmt.Errorf("escp: gateway %s must be another host address in %s", gw, subnet.String())
	}
	return nil
}

func broadcastOf(n net.IPNet) net.IP {
	out := make(net.IP, len(n.IP))
	for i := range n.IP {
		out[i] = n.IP[i] | ^n.Mask[i]
	}
	return out
}

// SetIPConfig changes the static management address, keeping the device
// description and the DHCP flag as the switch reports them.
//
// DANGER: this moves the switch. The request is answered from the old or the
// new address, so the client accepts replies from both and, once the switch
// confirms, follows it: later calls on this client go to the new address. If
// no reply arrives the new state is unknown (ErrNoReply); the caller must look
// at both addresses before it believes either. Callers must guard: a wrong
// address, mask or gateway can cut the management path for good.
func (c *Client) SetIPConfig(ctx context.Context, cfg IPConfig) error {
	if err := validateIPConfig(cfg, c.allowLoopback); err != nil {
		return err
	}
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	if _, err := ipConfigFromInfo(info); err != nil {
		return err
	}
	c.mu.Lock()
	c.alsoFrom = cfg.IP.To4()
	c.mu.Unlock()
	err = c.writeSystem(ctx, info.Hostname, info.DHCP, cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alsoFrom = nil
	if err != nil {
		return err
	}
	c.dest = &net.UDPAddr{IP: cfg.IP.To4(), Port: c.dest.Port}
	c.session = nil
	return c.awaitAddress(ctx, cfg)
}

// awaitAddress probes the new address until the switch reports it, because
// the first packets can fail while the neighbour caches of the network catch up
// (for example "host is down" from an earlier unanswered ARP). Callers hold c.mu.
func (c *Client) awaitAddress(ctx context.Context, want IPConfig) error {
	wait := c.moveWait
	if wait == 0 {
		wait = ipMoveWait
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		if info, err := c.discover(ctx); err == nil && info.IP == want.IP.String() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w (%s)", ErrMoveUnconfirmed, want.IP)
		case <-time.After(ipMovePoll):
		}
	}
}
