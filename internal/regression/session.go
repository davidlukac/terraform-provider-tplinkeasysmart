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
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
	"github.com/davidlukac/escp-go/model"

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
	Fake *escptest.Switch
	// MAC is the MAC address the target reports about itself.
	MAC    model.MAC
	before Readout
	// mode and mirror are the two settings the safety net puts back (see Rescue), as they were
	// when the session opened.
	mode   model.VLANModeSetting
	mirror model.Mirror
	// suite is the session's own client: it steps aside while the provider has the switch and is
	// dialed again at its next use (see suiteClient), so nothing keeps a *escp.Client of it; ask Client.
	suite *suiteClient
	// net is the client the safety net uses instead of the session's own, set by tests of its error paths.
	net rescueClient
	rd  []Reader
	tb  testing.TB
	// trace keeps what the library logs, for a failing scenario (see trace).
	trace *trace
	// lender hands the switch back and forth between the provider and the session's own client.
	lender
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
	s := &Session{Target: target, tb: t, trace: &trace{}}
	t.Cleanup(func() { s.trace.dumpIfFailed(t) }) // registered first, so it runs last
	cfg := escp.Config{Host: target.Host, Username: target.Username, Password: target.Password}
	if !target.Real() {
		s.Fake = startFake(t)
		if prepare != nil {
			prepare(s.Fake)
		}
		cfg = s.fakeConfig(cfg)
	}
	s.suite = &suiteClient{dial: func() (*escp.Client, error) { return s.dialClient(cfg) }}
	c, err := s.suite.get()
	if err != nil {
		t.Fatalf("cannot dial %s: %v", target.Name, err)
	}
	s.stepAside(s.suite)
	t.Cleanup(s.suite.release) // the run is over
	s.rd = CoreReaders(s.suite.get)
	waitUntilReady(t, target.Name, c, wallClock)
	err = s.capture(context.Background())
	if err == nil {
		s.before, err = Take(context.Background(), s.rd)
	}
	if err != nil {
		t.Fatalf("first readout of %s: %v", target.Name, err)
	}
	s.MAC, _ = model.ParseMAC(s.before["mac"]) // the readout prints MAC.String(), which always parses
	if err := CheckTestPorts(context.Background(), c, target); err != nil {
		t.Fatalf("%s: %v", target.Name, err)
	}
	return s
}

// Client is the session's own client, for checks that must not go through the provider. It is the
// client of the moment: the switch goes to the provider and back during a scenario, and each time
// the session's client steps aside a new one is dialed at the next use, so call Client at the point
// of use and do not keep the result across a step. It fails the test when no client can be dialed.
func (s *Session) Client() *escp.Client {
	s.tb.Helper()
	c, err := s.suite.get()
	if err != nil {
		s.tb.Fatalf("cannot dial a client of the suite's own for %s: %v", s.Target.Name, err)
	}
	return c
}

// rescuer is the client the safety net and the capture use: the session's own, or the stub of a test.
func (s *Session) rescuer() (rescueClient, error) {
	if s.net != nil {
		return s.net, nil
	}
	c, err := s.suite.get()
	if err != nil {
		return nil, fmt.Errorf("reaching the switch: %w", err)
	}
	return c, nil
}

// capture records the VLAN mode and the port mirror, as typed values the safety net can write back.
func (s *Session) capture(ctx context.Context) error {
	net, err := s.rescuer()
	if err != nil {
		return err
	}
	if s.mode, err = net.VLANMode(ctx); err != nil {
		return fmt.Errorf("reading the VLAN mode: %w", err)
	}
	if s.mirror, err = net.PortMirror(ctx); err != nil {
		return fmt.Errorf("reading the port mirror: %w", err)
	}
	return nil
}

// startFake starts the model of a freshly reset switch with the port shape of switch 03: the
// uplink on port 1 and devices on 6, 7 and 8 have a link, the other ports are free.
func startFake(t testing.TB) *escptest.Switch {
	f := escptest.Start(t)
	f.EnableModel(map[model.PortID]escptest.LinkSpeed{1: escptest.Link1000Full, 6: escptest.Link1000Full, 7: escptest.Link100Full, 8: escptest.Link100Full})
	return f
}

// fakeConfig aims a client configuration at the fake: its port, a free local port, loopback broadcast.
func (s *Session) fakeConfig(cfg escp.Config) escp.Config {
	cfg.SwitchPort = s.Fake.UDPPort()
	cfg.BroadcastAddr = escptest.Loopback()
	cfg.Timeout = fakeTimeout
	return cfg
}

// dialClient builds a client that logs into the session's trace; for the fake each client gets its own local port.
func (s *Session) dialClient(cfg escp.Config) (*escp.Client, error) {
	cfg.Logger = s.trace.logger()
	if s.Fake != nil {
		if cfg.Host == "" && !cfg.MAC.IsZero() {
			// A client without an address finds its switch by a discovery on the host's interfaces,
			// which cannot see a fake on loopback. This stands in for it: the fake is found when the
			// MAC is its own, and nothing is found otherwise.
			if cfg.MAC != escptest.DefaultMAC() {
				return nil, fmt.Errorf("%w: %s", escp.ErrNotFound, cfg.MAC)
			}
			cfg.Host = loopback
		}
		cfg = s.fakeConfig(cfg)
		cfg.HostPort = escptest.FreeUDPPort(s.tb)
	}
	return escp.Dial(cfg)
}

// dial is dialClient for the provider; see lender.dial.
func (s *Session) dial(cfg escp.Config) (provider.SwitchClient, error) {
	return s.lender.dial(cfg, s.dialClient)
}

// own takes the switch back from the provider for the session's own client; see lender.takeBack.
func (s *Session) own() { s.takeBack() }

// test is resource.Test for the session's provider (or the factories the case names), with the
// switch handed back and forth; see lender.run.
func (s *Session) test(t *testing.T, tc resource.TestCase) {
	t.Helper()
	if tc.ProtoV6ProviderFactories == nil {
		tc.ProtoV6ProviderFactories = s.Factories()
	}
	s.run(t, tc)
}

// Factories returns the real provider for terraform-plugin-testing, dialing the target.
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
func (s *Session) ProviderHCLByMAC(host string, mac model.MAC) string {
	hostLine := ""
	if host != "" {
		hostLine = fmt.Sprintf("  host     = %q\n", host)
	}
	return fmt.Sprintf("provider \"tplinkeasysmart\" {\n%s  mac      = %q\n  username = %q\n  password = %q\n}\n", hostLine, mac.String(), s.Target.Username, s.Target.Password.Reveal())
}

// ProviderHCLByMACRelocating is ProviderHCLByMAC with relocate_by_mac on: a switch named by host and
// MAC is looked up by its MAC when nothing answers at host (the library keeps to host otherwise).
func (s *Session) ProviderHCLByMACRelocating(host string, mac model.MAC) string {
	return strings.Replace(s.ProviderHCLByMAC(host, mac), "}\n", "  relocate_by_mac = true\n}\n", 1)
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
