package regression

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// loopback is where the fakes listen.
const loopback = "127.0.0.1"

// fakeTimeout is how long the clients of the fake wait per attempt: it answers at once, so a
// silence is a bug, not a slow management CPU.
const fakeTimeout = 500 * time.Millisecond

// Session is one run against one target: the readout taken before any scenario, the provider
// aimed at the target, and a client of the suite's own for checks that must not go through the
// provider.
type Session struct {
	Target Target
	// Fake is the in-process switch, nil on hardware.
	Fake   *escptest.Switch
	Client *escp.Client
	// MAC is the MAC address the target reports about itself.
	MAC    escp.MAC
	before Readout
	// mode and mirror are the two settings the safety net puts back (see Rescue), as they were
	// when the session opened.
	mode   escp.VLANModeSetting
	mirror escp.Mirror
	// net is the client the safety net uses; the session's own, replaceable in tests.
	net rescueClient
	rd  []Reader
	tb  testing.TB
}

// Getenv is os.Getenv, replaceable in tests.
type Getenv func(string) string

// Open loads the target from the environment (and from the file named by TPLINK_REGRESSION_ENV_FILE,
// the gitignored .test.env), prepares it and takes the first readout. Anything wrong with the
// target fails the test before a single write.
func Open(t testing.TB, getenv Getenv) *Session { return openWith(t, getenv, nil) }

// openWith is Open with a hook that may change the fake before the first readout (tests only).
func openWith(t testing.TB, getenv Getenv, prepare func(*escptest.Switch)) *Session {
	t.Helper()
	env := map[string]string{}
	if path := getenv(EnvFile); path != "" {
		f, err := os.Open(path) //nolint:gosec // the path is the operator's own file
		if err != nil {
			t.Fatalf("cannot read %s: %v", EnvFile, err)
		}
		fileEnv, err := ParseEnvFile(f)
		_ = f.Close() // read-only, nothing to flush
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		env = fileEnv
	}
	if v := getenv(EnvTarget); v != "" {
		env[EnvTarget] = v
	}
	target, err := LoadTarget(env)
	if err != nil {
		t.Fatalf("regression target: %v", err)
	}
	s := &Session{Target: target, tb: t}
	cfg := escp.Config{Host: target.Host, Username: target.Username, Password: target.Password}
	if !target.Real() {
		s.Fake = startFake(t)
		if prepare != nil {
			prepare(s.Fake)
		}
		cfg = s.fakeConfig(cfg)
	}
	c, err := s.dialClient(cfg)
	if err != nil {
		t.Fatalf("cannot dial %s: %v", target.Name, err)
	}
	s.Client, s.net = c, c
	t.Cleanup(func() { _ = s.Client.Close() }) // best effort: the run is over
	s.rd = CoreReaders(s.Client)
	err = s.capture(context.Background())
	if err == nil {
		s.before, err = Take(context.Background(), s.rd)
	}
	if err != nil {
		t.Fatalf("first readout of %s: %v", target.Name, err)
	}
	s.MAC, _ = escp.ParseMAC(s.before["mac"]) // the readout prints MAC.String(), which always parses
	if err := CheckTestPorts(context.Background(), s.Client, target); err != nil {
		t.Fatalf("%s: %v", target.Name, err)
	}
	return s
}

// capture records the VLAN mode and the port mirror, as typed values the safety net can write back.
func (s *Session) capture(ctx context.Context) (err error) {
	if s.mode, err = s.net.VLANMode(ctx); err != nil {
		return fmt.Errorf("reading the VLAN mode: %w", err)
	}
	if s.mirror, err = s.net.PortMirror(ctx); err != nil {
		return fmt.Errorf("reading the port mirror: %w", err)
	}
	return nil
}

// startFake starts the model of a freshly reset switch with the port shape of switch 03: the
// uplink on port 1 and devices on 6, 7 and 8 have a link, the other ports are free.
func startFake(t testing.TB) *escptest.Switch {
	f := escptest.Start(t)
	f.EnableModel(map[int]escptest.LinkSpeed{1: escptest.Link1000Full, 6: escptest.Link1000Full, 7: escptest.Link100Full, 8: escptest.Link100Full})
	return f
}

// fakeConfig aims a client configuration at the fake: its port, a free local port, loopback broadcast.
func (s *Session) fakeConfig(cfg escp.Config) escp.Config {
	cfg.SwitchPort = s.Fake.Port()
	cfg.BroadcastAddr = escptest.Loopback
	cfg.Timeout = fakeTimeout
	return cfg
}

// dialClient builds a client; for the fake each client gets its own local port.
func (s *Session) dialClient(cfg escp.Config) (*escp.Client, error) {
	if s.Fake != nil {
		if cfg.Host == "" && !cfg.MAC.IsZero() {
			// A client without an address finds its switch by a discovery on the host's interfaces,
			// which cannot see a fake on loopback. This stands in for it: the fake is found when the
			// MAC is its own, and nothing is found otherwise.
			if cfg.MAC != escp.MAC(escptest.DefaultMAC) {
				return nil, fmt.Errorf("%w: %s", escp.ErrNotFound, cfg.MAC)
			}
			cfg.Host = loopback
		}
		cfg = s.fakeConfig(cfg)
		cfg.HostPort = escptest.FreeUDPPort(s.tb)
	}
	return escp.Dial(cfg)
}

// dial is dialClient for the provider.
func (s *Session) dial(cfg escp.Config) (provider.SwitchClient, error) {
	return s.dialClient(cfg)
}

// Factories returns the real provider for terraform-plugin-testing, dialling the target.
func (s *Session) Factories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(provider.NewWithDialer("regression", s.dial)()),
	}
}

// ProviderHCL is the provider block of every scenario's configuration.
func (s *Session) ProviderHCL() string {
	return fmt.Sprintf("provider \"tplinkeasysmart\" {\n  host     = %q\n  username = %q\n  password = %q\n}\n",
		s.Target.Host, s.Target.Username, s.Target.Password.Reveal())
}

// ProviderHCLByMAC is the provider block naming the switch by its MAC address only: no address
// anywhere, so the provider has to find it. host, when not empty, is added as an address to try first.
func (s *Session) ProviderHCLByMAC(host string, mac escp.MAC) string {
	hostLine := ""
	if host != "" {
		hostLine = fmt.Sprintf("  host     = %q\n", host)
	}
	return fmt.Sprintf("provider \"tplinkeasysmart\" {\n%s  mac      = %q\n  username = %q\n  password = %q\n}\n", hostLine, mac.String(), s.Target.Username, s.Target.Password.Reveal())
}

// Before is the readout taken when the session opened: what every scenario must leave behind.
func (s *Session) Before() Readout { return s.before }

// Restored reads the switch again and returns the differences from Before, empty when identical.
func (s *Session) Restored(ctx context.Context) ([]string, error) {
	after, err := Take(ctx, s.rd)
	if err != nil {
		return nil, err
	}
	return Diff(s.before, after), nil
}

// Describe says which target a run used, for the log and the report; it never includes the password.
func (s *Session) Describe() string {
	return fmt.Sprintf("target %s (%s), test ports %s", s.Target.Name, s.Target.Host, strings.Trim(fmt.Sprint(s.Target.TestPorts), "[]"))
}
