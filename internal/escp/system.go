package escp

import (
	"context"
	"fmt"
	"net"
	"regexp"
)

// TLV types of the System page that are not part of discovery.
const (
	TLVPortCount = 10 // number of physical ports (1 byte)
	TLVAutoSave  = 13 // 1 = configuration changes are saved automatically (1 byte)
)

// System holds device facts that are not part of discovery.
type System struct {
	PortCount int
	AutoSave  bool
}

func parseSystem(ports, auto []TLV) (System, error) {
	var s System
	found := 0
	for _, t := range ports {
		if t.Type == TLVPortCount && len(t.Value) == 1 {
			s.PortCount, found = int(t.Value[0]), found+1
		}
	}
	for _, t := range auto {
		if t.Type == TLVAutoSave && len(t.Value) == 1 {
			s.AutoSave, found = t.Value[0] == 1, found+1
		}
	}
	if found != 2 {
		return s, fmt.Errorf("escp: incomplete system reply")
	}
	return s, nil
}

// System reads the port count and the auto-save flag.
func (c *Client) System(ctx context.Context) (System, error) {
	ports, err := c.getTLVs(ctx, TLVPortCount)
	if err != nil {
		return System{}, err
	}
	auto, err := c.getTLVs(ctx, TLVAutoSave)
	if err != nil {
		return System{}, err
	}
	return parseSystem(ports, auto)
}

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

// ValidateHostname applies the web UI's rules for the device description.
func ValidateHostname(name string) error {
	if len(name) > 32 || !hostnameRE.MatchString(name) {
		return fmt.Errorf("escp: invalid device description %q: 1-32 letters, digits and hyphens, not starting or ending with a hyphen", name)
	}
	return nil
}

// SetHostname changes the device description. NOT verified live: the same-value
// write was blocked by the permission classifier, so no resource uses it yet.
// Like the vendor utility, the
// request carries the current IP settings (read via discovery) unchanged, so
// the management address is re-asserted as it is.
func (c *Client) SetHostname(ctx context.Context, name string) error {
	if err := ValidateHostname(name); err != nil {
		return err
	}
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	ip, mask, gw := net.ParseIP(info.IP).To4(), net.ParseIP(info.Netmask).To4(), net.ParseIP(info.Gateway).To4()
	if ip == nil || mask == nil || gw == nil {
		return fmt.Errorf("escp: switch reported an unusable IP configuration (%q %q %q); refusing to write", info.IP, info.Netmask, info.Gateway)
	}
	return c.setTLVs(ctx,
		TLV{TLVHostname, append([]byte(name), 0)},
		TLV{TLVDHCP, []byte{boolByte(info.DHCP)}},
		TLV{TLVIP, ip}, TLV{TLVNetmask, mask}, TLV{TLVGateway, gw})
}
