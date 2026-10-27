package regression

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// The targets a run can be aimed at.
const (
	// TargetFake is the in-process fake switch (the default).
	TargetFake = "fake"
	// TargetSW03 is switch 03, the only real switch the suite may write to.
	TargetSW03 = "sw03"
	// targetSW01 is named only to be refused.
	targetSW01 = "sw01"
)

// Keys of the environment (or .test.env file) that select and describe the target.
const (
	EnvTarget         = "TPLINK_REGRESSION_TARGET"
	EnvFile           = "TPLINK_REGRESSION_ENV_FILE"
	envHost           = "TPLINK_REGRESSION_HOST"
	envUsername       = "TPLINK_REGRESSION_USERNAME"
	envPassword       = "TPLINK_REGRESSION_PASSWORD" //nolint:gosec // the name of a variable, not a credential
	envTestPorts      = "TPLINK_REGRESSION_TEST_PORTS"
	envProtectedPorts = "TPLINK_REGRESSION_PROTECTED_PORTS"
	envReadOnlyHosts  = "TPLINK_REGRESSION_READONLY_HOSTS"
	// EnvAllowMove ("1") and envMoveIP opt in to the scenario that moves the switch to a spare
	// address and back. The spare address must be free; it is never probed before the move.
	EnvAllowMove = "TPLINK_REGRESSION_ALLOW_MANAGEMENT_MOVE"
	envMoveIP    = "TPLINK_REGRESSION_MOVE_IP"
	// EnvAllowDHCP ("1") opts in to the scenario that turns DHCP on and back off. The router's DHCP
	// server must be up: the switch takes whatever address it gives, and is found again by its MAC.
	EnvAllowDHCP = "TPLINK_REGRESSION_ALLOW_DHCP"
	// envDisruptPort opts in to the allow_disruptive scenario writing to this device port (a brief
	// link drop). It must be a protected port other than the first, which is the uplink.
	envDisruptPort = "TPLINK_REGRESSION_DISRUPT_DEVICE_PORT"
	// envDisruptivePorts opts in to the disruptive scenarios (real changes behind allow_disruptive on
	// ports that carry a device). The ports must be protected ports other than the uplink.
	envDisruptivePorts = "TPLINK_REGRESSION_DISRUPTIVE_PORTS"
)

// Target says what a run talks to and what it may touch.
type Target struct {
	Name     string
	Host     string
	Username string
	Password escp.Password
	// TestPorts are the ports a scenario may change: free, link-down, in no LAG.
	TestPorts []escp.PortID
	// ProtectedPorts (the uplink and the ports with devices) are named only by guard-refusal scenarios.
	ProtectedPorts []escp.PortID
	// MoveIP is the spare address the management-move scenario may use; empty unless the run opted in.
	MoveIP string
	// DHCP is true when the run opted in to the DHCP scenario.
	DHCP bool
	// DisruptPort is the device port the allow_disruptive scenario may write to; zero unless the run opted in.
	DisruptPort escp.PortID
	// DisruptivePorts are the device ports the disruptive scenarios may change for real (a link
	// drop, a LAG, a VLAN, a mirror destination); empty unless the run opted in. Never the uplink.
	DisruptivePorts []escp.PortID
}

// Real reports whether the target is hardware.
func (t Target) Real() bool { return t.Name != TargetFake }

// ParseEnvFile reads KEY=VALUE lines (blank lines and # comments ignored, optional quotes around
// the value) into a map. It never echoes a value, so a password cannot reach an error message.
func ParseEnvFile(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("line %d is not KEY=VALUE", n)
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out, sc.Err()
}

// LoadTarget builds the target from env. The fake needs nothing. Anything else must be switch 03
// and must name its host, credentials and ports; switch 01 is refused by name and by address
// (the addresses in TPLINK_REGRESSION_READONLY_HOSTS, comma separated), and a port may not be
// both a test port and a protected one.
func LoadTarget(env map[string]string) (Target, error) {
	name := env[EnvTarget]
	switch name {
	case "", TargetFake:
		return Target{Name: TargetFake, Host: "127.0.0.1", Username: "admin", Password: "secret",
			TestPorts: []escp.PortID{2, 3, 4, 5}, ProtectedPorts: []escp.PortID{1, 6, 7, 8}, DisruptivePorts: []escp.PortID{7, 8}}, nil
	case TargetSW03:
	case targetSW01:
		return Target{}, errors.New("switch 01 is read-only for the regression suite and is never a target")
	default:
		return Target{}, fmt.Errorf("unknown target %q (use %s or %s)", name, TargetFake, TargetSW03)
	}
	t := Target{Name: name, Host: env[envHost], Username: env[envUsername], Password: escp.Password(env[envPassword])}
	for key, v := range map[string]string{envHost: t.Host, envUsername: t.Username, envPassword: env[envPassword]} {
		if v == "" {
			return Target{}, fmt.Errorf("%s is not set for target %s", key, name)
		}
	}
	for _, h := range strings.Split(env[envReadOnlyHosts], ",") {
		if h = strings.TrimSpace(h); h != "" && h == t.Host {
			return Target{}, fmt.Errorf("host %s is a read-only switch and is never a target", t.Host)
		}
	}
	var err error
	if t.TestPorts, err = parsePorts(env[envTestPorts]); err != nil {
		return Target{}, fmt.Errorf("%s: %w", envTestPorts, err)
	}
	if len(t.TestPorts) == 0 {
		return Target{}, fmt.Errorf("%s names no test port", envTestPorts)
	}
	if t.ProtectedPorts, err = parsePorts(env[envProtectedPorts]); err != nil {
		return Target{}, fmt.Errorf("%s: %w", envProtectedPorts, err)
	}
	for _, p := range t.TestPorts {
		for _, q := range t.ProtectedPorts {
			if p == q {
				return Target{}, fmt.Errorf("port %d is both a test port and a protected port", p)
			}
		}
	}
	if env[EnvAllowMove] == "1" {
		if t.MoveIP, err = moveIP(env[envMoveIP], t.Host, env[envReadOnlyHosts]); err != nil {
			return Target{}, fmt.Errorf("%s: %w", envMoveIP, err)
		}
	}
	t.DHCP = env[EnvAllowDHCP] == "1"
	if v := env[envDisruptPort]; v != "" {
		ports, err := parsePorts(v)
		if err != nil || len(ports) != 1 {
			return Target{}, fmt.Errorf("%s: %q is not one port number", envDisruptPort, v)
		}
		if !slices.Contains(t.ProtectedPorts[min(1, len(t.ProtectedPorts)):], ports[0]) {
			return Target{}, fmt.Errorf("%s: port %d is not a device port (the protected ports other than the uplink, which is listed first, are %v)", envDisruptPort, ports[0], t.ProtectedPorts[min(1, len(t.ProtectedPorts)):])
		}
		t.DisruptPort = ports[0]
	}
	if v := env[envDisruptivePorts]; v != "" {
		ports, err := parsePorts(v)
		if err != nil || len(ports) == 0 {
			return Target{}, fmt.Errorf("%s: %q is not a list of port numbers", envDisruptivePorts, v)
		}
		devices := t.ProtectedPorts[min(1, len(t.ProtectedPorts)):]
		for _, p := range ports {
			if !slices.Contains(devices, p) {
				return Target{}, fmt.Errorf("%s: port %d is not a device port (the protected ports other than the uplink, which is listed first, are %v)", envDisruptivePorts, p, devices)
			}
		}
		t.DisruptivePorts = ports
	}
	return t, nil
}

// moveIP checks the spare address for the management move: an IPv4 address that is neither the
// switch's own nor a read-only switch's.
func moveIP(ip, host, readOnly string) (string, error) {
	parsed := net.ParseIP(ip).To4()
	if parsed == nil {
		return "", fmt.Errorf("%q is not an IPv4 address", ip)
	}
	if ip == host {
		return "", errors.New("the spare address is the switch's own address")
	}
	for _, h := range strings.Split(readOnly, ",") {
		if strings.TrimSpace(h) == ip {
			return "", fmt.Errorf("%s is a read-only switch", ip)
		}
	}
	return ip, nil
}

func parsePorts(s string) ([]escp.PortID, error) {
	var out []escp.PortID
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		p, err := escp.ParsePortID(f)
		if err != nil {
			return nil, fmt.Errorf("%q is not a port number", f)
		}
		out = append(out, p)
	}
	return out, nil
}
