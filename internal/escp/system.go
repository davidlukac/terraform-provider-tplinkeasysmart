package escp

import (
	"context"
	"fmt"
	"regexp"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// System holds device facts that are not part of discovery.
type System struct {
	PortCount int
	AutoSave  bool
}

func parseSystem(ports, auto []wire.TLV) (System, error) {
	var s System
	found := 0
	for _, t := range ports {
		if t.Type == wire.TLVPortCount && len(t.Value) == 1 {
			s.PortCount, found = int(t.Value[0]), found+1
		}
	}
	for _, t := range auto {
		if t.Type == wire.TLVAutoSave && len(t.Value) == 1 {
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
	ports, err := c.getTLVs(ctx, wire.TLVPortCount)
	if err != nil {
		return System{}, err
	}
	auto, err := c.getTLVs(ctx, wire.TLVAutoSave)
	if err != nil {
		return System{}, err
	}
	return parseSystem(ports, auto)
}

// Hostname is the device description of a switch (System > System Info), what the web UI calls the
// device description and the protocol the hostname: 1 to MaxHostnameLen letters, digits and
// hyphens, not starting or ending with a hyphen. The rules are the web UI's.
type Hostname string

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

// Validate applies the rules; the error matches ErrInvalid.
func (h Hostname) Validate() error {
	if len(h) > MaxHostnameLen || !hostnameRE.MatchString(string(h)) {
		return invalidf("escp: invalid device description %q: 1-%d letters, digits and hyphens, not starting or ending with a hyphen", string(h), MaxHostnameLen)
	}
	return nil
}

// String is the device description.
func (h Hostname) String() string { return string(h) }

// SetHostname changes the device description. Like the vendor utility, the
// request carries the current IP settings (read via discovery) unchanged, so
// the management address is re-asserted as it is. Verified live on firmware
// 20230218 and 20250710.
func (c *Client) SetHostname(ctx context.Context, name Hostname) error {
	if err := name.Validate(); err != nil {
		return err
	}
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	cur, err := ipConfigFromInfo(info)
	if err != nil {
		return err
	}
	_, err = c.writeSystem(ctx, Operation{Name: "SetHostname", Impact: ImpactCosmetic}, name, info.DHCP, cur)
	return err
}

// writeSystem sends the System-page SET: device description, DHCP flag and the
// static address triple. The switch takes all of them in one request.
func (c *Client) writeSystem(ctx context.Context, op Operation, name Hostname, dhcp bool, cfg IPConfig) (sent bool, err error) {
	return c.setTLVsSent(ctx, op,
		wire.TLV{Type: wire.TLVHostname, Value: append([]byte(name), 0)},
		wire.TLV{Type: wire.TLVDHCP, Value: []byte{boolByte(dhcp)}},
		wire.TLV{Type: wire.TLVIP, Value: cfg.IP.AsSlice()}, wire.TLV{Type: wire.TLVNetmask, Value: cfg.Netmask.AsSlice()}, wire.TLV{Type: wire.TLVGateway, Value: cfg.Gateway.AsSlice()})
}
