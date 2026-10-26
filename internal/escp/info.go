package escp

import (
	"context"
	"net"

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
