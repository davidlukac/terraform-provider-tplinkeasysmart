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

// HostnamePattern is the regular expression a device description must match (besides the length limit).
const HostnamePattern = `^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`

var hostnameRE = regexp.MustCompile(HostnamePattern)

// ValidateHostname applies the web UI's rules for the device description.
func ValidateHostname(name string) error {
	if len(name) > MaxHostnameLen || !hostnameRE.MatchString(name) {
		return invalidf("escp: invalid device description %q: 1-%d letters, digits and hyphens, not starting or ending with a hyphen", name, MaxHostnameLen)
	}
	return nil
}

// SetHostname changes the device description. Like the vendor utility, the
// request carries the current IP settings (read via discovery) unchanged, so
// the management address is re-asserted as it is. Verified live on firmware
// 20230218 and 20250710.
func (c *Client) SetHostname(ctx context.Context, name string) error {
	if err := ValidateHostname(name); err != nil {
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
	return c.writeSystem(ctx, Operation{Name: "SetHostname", Impact: ImpactCosmetic}, name, info.DHCP, cur)
}

// writeSystem sends the System-page SET: device description, DHCP flag and the
// static address triple. The switch takes all of them in one request.
func (c *Client) writeSystem(ctx context.Context, op Operation, name string, dhcp bool, cfg IPConfig) error {
	return c.setTLVs(ctx, op,
		wire.TLV{Type: wire.TLVHostname, Value: append([]byte(name), 0)},
		wire.TLV{Type: wire.TLVDHCP, Value: []byte{boolByte(dhcp)}},
		wire.TLV{Type: wire.TLVIP, Value: cfg.IP.To4()}, wire.TLV{Type: wire.TLVNetmask, Value: cfg.Netmask.To4()}, wire.TLV{Type: wire.TLVGateway, Value: cfg.Gateway.To4()})
}
