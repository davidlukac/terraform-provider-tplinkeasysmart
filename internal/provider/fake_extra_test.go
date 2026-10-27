package provider

import (
	"context"
	"errors"
	"net/netip"
	"sync"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// extraState holds the fake switch behaviour for the device description and
// the configuration backup. It lives beside fakeClient (keyed by pointer) so
// the main fake stays small.
type extraState struct {
	mu              sync.Mutex
	active          bool // when false, Info falls back to the static fake data
	hostname        string
	setCalls        int
	setErr          error
	ignoreSet       bool  // accept the write but keep the old name, like a device that silently ignores a setting
	mutateIP        bool  // simulate a write that corrupts the management IP
	infoErr         error // Info fails
	infoErrAfterSet bool  // Info starts failing once a write happened (read-back fails)
	backup          []byte
	backupErr       error
	ip              string
	netmask         string // empty means 255.255.255.0
	gateway         string // empty means 192.0.2.1
	static          bool   // the switch uses a static address (DHCP off); the default fake leases one
	ipSetCalls      int
	ipSetErr        error
	ipIgnoreSet     bool      // accept SetIPConfig but keep the old address
	dhcpCalls       int       // EnableDHCP calls
	dhcpErr         error     // EnableDHCP fails
	dhcpIgnore      bool      // EnableDHCP is accepted but the switch stays static
	lease           [3]string // ip, mask and gateway the fake leases; empty means 192.0.2.85 / 255.255.255.0 / 192.0.2.1
	firmware        string    // the firmware version Info reports; empty means defaultFakeFirmware
}

// fakeMAC is the MAC address every fake client reports.
var fakeMAC = escp.MAC{0x00, 0x00, 0x5e, 0x00, 0x53, 0x01}

// defaultFakeFirmware is the build the fakes report unless a test sets another: the verified baseline.
const defaultFakeFirmware = "1.0.0 Build 20230218 Rel.50633"

func (e *extraState) firmwareVersion() string {
	if e.firmware == "" {
		return defaultFakeFirmware
	}
	return e.firmware
}

// setFirmware makes Info report the given firmware version.
func (f *fakeClient) setFirmware(version string) {
	e := f.extra()
	e.mu.Lock()
	e.firmware = version
	e.mu.Unlock()
}

var (
	extrasMu sync.Mutex
	extras   = map[*fakeClient]*extraState{}
)

func (f *fakeClient) extra() *extraState {
	extrasMu.Lock()
	defer extrasMu.Unlock()
	if e, ok := extras[f]; ok {
		return e
	}
	e := &extraState{ip: "192.0.2.120"}
	extras[f] = e
	return e
}

func (e *extraState) info() (escp.Info, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.infoErr != nil {
		return escp.Info{}, true, e.infoErr
	}
	if !e.active {
		return escp.Info{Firmware: escp.ParseFirmware(e.firmwareVersion())}, false, nil
	}
	return escp.Info{Model: "TL-SG108E", Hostname: e.hostname, MAC: fakeMAC, Firmware: escp.ParseFirmware(e.firmwareVersion()),
		IP: netip.MustParseAddr(e.ip), Netmask: netip.MustParseAddr(orDefault(e.netmask, "255.255.255.0")), Gateway: netip.MustParseAddr(orDefault(e.gateway, "192.0.2.1")), DHCP: !e.static}, true, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// SetIPConfig moves the fake switch like the real one: the new triple is what Info reports afterwards.
func (f *fakeClient) SetIPConfig(_ context.Context, cfg escp.IPConfig) error {
	if err := escp.ValidateIPConfig(cfg); err != nil {
		return err
	}
	e := f.extra()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ipSetCalls++
	if e.ipSetErr != nil {
		return e.ipSetErr
	}
	if !e.ipIgnoreSet {
		e.ip, e.netmask, e.gateway = cfg.IP.String(), cfg.Netmask.String(), cfg.Gateway.String()
		e.static = true // writing a static address turns DHCP off
	}
	return nil
}

// EnableDHCP makes the fake switch lease an address, like the real one: Info reports the lease afterwards.
func (f *fakeClient) EnableDHCP(context.Context) error {
	e := f.extra()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dhcpCalls++
	if e.dhcpErr != nil {
		return e.dhcpErr
	}
	if !e.dhcpIgnore {
		e.static = false
		e.ip, e.netmask, e.gateway = orDefault(e.lease[0], "192.0.2.85"), orDefault(e.lease[1], "255.255.255.0"), orDefault(e.lease[2], "192.0.2.1")
	}
	return nil
}

// newStaticFake returns a fake switch with a static address, as switch 03 has.
func newStaticFake(ip, mask, gateway string) *fakeClient {
	f := &fakeClient{}
	e := f.extra()
	e.active, e.static, e.hostname, e.ip, e.netmask, e.gateway = true, true, "TL-SG108E-03", ip, mask, gateway
	return f
}

func (f *fakeClient) SetHostname(_ context.Context, name string) error {
	if err := escp.ValidateHostname(name); err != nil {
		return err
	}
	e := f.extra()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.setCalls++
	if e.infoErrAfterSet {
		e.infoErr = errBoom
	}
	if e.setErr != nil {
		return e.setErr
	}
	if !e.ignoreSet {
		e.hostname = name
	}
	if e.mutateIP {
		e.ip = "192.0.2.250"
	}
	return nil
}

func (f *fakeClient) ConfigBackup(context.Context) ([]byte, error) {
	e := f.extra()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.backupErr != nil {
		return nil, e.backupErr
	}
	return append([]byte(nil), e.backup...), nil
}

// newHostnameFake returns a fake whose device description starts as name.
func newHostnameFake(name string) *fakeClient {
	f := &fakeClient{}
	e := f.extra()
	e.active, e.hostname = true, name
	return f
}

var errBoom = errors.New("boom")
