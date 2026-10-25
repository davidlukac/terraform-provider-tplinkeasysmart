package regression

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
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
)

// Target says what a run talks to and what it may touch.
type Target struct {
	Name     string
	Host     string
	Username string
	Password string
	// TestPorts are the ports a scenario may change: free, link-down, in no LAG.
	TestPorts []int
	// ProtectedPorts (the uplink and the ports with devices) are named only by guard-refusal scenarios.
	ProtectedPorts []int
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
		return Target{Name: TargetFake, Host: "127.0.0.1", Username: "admin", Password: "secret"}, nil
	case TargetSW03:
	case targetSW01:
		return Target{}, errors.New("switch 01 is read-only for the regression suite and is never a target")
	default:
		return Target{}, fmt.Errorf("unknown target %q (use %s or %s)", name, TargetFake, TargetSW03)
	}
	t := Target{Name: name, Host: env[envHost], Username: env[envUsername], Password: env[envPassword]}
	for key, v := range map[string]string{envHost: t.Host, envUsername: t.Username, envPassword: t.Password} {
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
	return t, nil
}

func parsePorts(s string) ([]int, error) {
	var out []int
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("%q is not a port number", f)
		}
		out = append(out, n)
	}
	return out, nil
}
