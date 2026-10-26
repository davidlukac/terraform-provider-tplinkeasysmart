package regression

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

func noEnv(string) string { return "" }

func TestOpenFakeReadsAndDetectsChange(t *testing.T) {
	s := Open(t, noEnv)
	if s.Fake == nil || s.Target.Real() {
		t.Fatalf("session = %+v, want the fake", s.Target)
	}
	if got := s.Before()["led"]; got != "true" {
		t.Errorf("led before = %q, want true", got)
	}
	if got := s.Before()["igmp"]; got != "enabled=true suppression=false" {
		t.Errorf("igmp before = %q", got)
	}
	ctx := context.Background()
	if diff, err := s.Restored(ctx); err != nil || len(diff) != 0 {
		t.Fatalf("untouched switch: diff %q, err %v", diff, err)
	}
	if err := s.Client.SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	diff, err := s.Restored(ctx)
	if err != nil || len(diff) != 1 || !strings.HasPrefix(diff[0], "led:") {
		t.Errorf("after a write: diff %q, err %v, want the led named", diff, err)
	}
}

func TestOpenReadsWritesBack(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	if err := s.Client.SetIGMP(ctx, igmpConfig(false, true)); err != nil {
		t.Fatal(err)
	}
	diff, err := s.Restored(ctx)
	if err != nil || len(diff) != 1 || !strings.Contains(diff[0], "enabled=false suppression=true") {
		t.Errorf("diff %q, err %v", diff, err)
	}
}

func TestSessionDescribeAndHCL(t *testing.T) {
	s := Open(t, noEnv)
	if got := s.Describe(); got != "target fake (127.0.0.1), test ports 2 3 4 5" {
		t.Errorf("Describe = %q", got)
	}
	if hcl := s.ProviderHCL(); !strings.Contains(hcl, `host     = "127.0.0.1"`) || !strings.Contains(hcl, `password = "secret"`) {
		t.Errorf("ProviderHCL = %q", hcl)
	}
	if len(s.Factories()) != 1 {
		t.Error("expected one provider factory")
	}
}

func TestOpenFileErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.env")
	if err := os.WriteFile(bad, []byte("no equals sign\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"missing": filepath.Join(dir, "none.env"), "malformed": bad} {
		t.Run(name, func(t *testing.T) {
			ft := &fatalRecorder{TB: t}
			func() {
				defer func() { _ = recover() }()
				Open(ft, func(k string) string {
					if k == EnvFile {
						return path
					}
					return ""
				})
			}()
			if !ft.failed {
				t.Error("Open did not fail on an unusable env file")
			}
		})
	}
}

func TestOpenRefusesBadTarget(t *testing.T) {
	ft := &fatalRecorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		Open(ft, func(k string) string {
			if k == EnvTarget {
				return "sw01"
			}
			return ""
		})
	}()
	if !ft.failed || !strings.Contains(ft.msg, "never a target") {
		t.Errorf("failed=%t msg=%q, want a refusal of switch 01", ft.failed, ft.msg)
	}
}

func TestOpenEnvFileSelectsTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".test.env")
	if err := os.WriteFile(path, []byte("TPLINK_REGRESSION_TARGET=fake\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(t, func(k string) string {
		if k == EnvFile {
			return path
		}
		return ""
	})
	if s.Target.Name != TargetFake {
		t.Errorf("target = %s", s.Target.Name)
	}
}

func TestFailingReadsStopTheRun(t *testing.T) {
	failReads := func(f *escptest.Switch) {
		f.Configure(func(f *escptest.Switch) {
			f.GetFn = func([]int16) ([]escptest.Setting, int32, bool) { return nil, 1, false }
		})
	}
	// Restored cannot say whether the switch is as it was when it cannot read it.
	s := Open(t, noEnv)
	failReads(s.Fake)
	if diff, err := s.Restored(context.Background()); err == nil {
		t.Errorf("Restored = %q, nil; want the read error", diff)
	}
	// A first readout that fails stops the run before any scenario.
	ft := &fatalRecorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		openWith(ft, noEnv, failReads)
	}()
	if !ft.failed || !strings.Contains(ft.msg, "first readout") {
		t.Errorf("failed=%t msg=%q, want the first readout to fail the run", ft.failed, ft.msg)
	}
}

func TestOpenFailsOnAnUnresolvableHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".test.env")
	env := "TPLINK_REGRESSION_TARGET=sw03\nTPLINK_REGRESSION_HOST=not a host\nTPLINK_REGRESSION_USERNAME=u\nTPLINK_REGRESSION_PASSWORD=p\nTPLINK_REGRESSION_TEST_PORTS=2\n"
	if err := os.WriteFile(path, []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	ft := &fatalRecorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		Open(ft, func(k string) string {
			if k == EnvFile {
				return path
			}
			return ""
		})
	}()
	if !ft.failed || !strings.Contains(ft.msg, "cannot dial sw03") || strings.Contains(ft.msg, "p\n") {
		t.Errorf("failed=%t msg=%q", ft.failed, ft.msg)
	}
}

func TestProviderDialAimsAtTheFake(t *testing.T) {
	s := Open(t, noEnv)
	c, err := s.dial(escp.Config{Host: s.Target.Host, Username: s.Target.Username, Password: s.Target.Password})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.(*escp.Client).Close() }()
	if on, err := c.LED(context.Background()); err != nil || !on {
		t.Errorf("LED through the provider's dialer = %t, %v", on, err)
	}
}

func TestOpenStopsWhenATestPortHasALink(t *testing.T) {
	ft := &fatalRecorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		openWith(ft, noEnv, func(f *escptest.Switch) {
			f.EnableModel(map[int]escptest.LinkSpeed{3: escptest.Link100Full})
		})
	}()
	if !ft.failed || !strings.Contains(ft.msg, "test port 3 has a link") {
		t.Errorf("failed=%t msg=%q, want the run stopped before any write", ft.failed, ft.msg)
	}
}

func TestAClientWithOnlyAMACFindsTheFakeWhenTheMACIsItsOwn(t *testing.T) {
	s := Open(t, noEnv)
	if s.MAC != net.HardwareAddr(escptest.DefaultMAC[:]).String() {
		t.Fatalf("the session reports MAC %q, want the fake's %v", s.MAC, escptest.DefaultMAC)
	}
	hw, err := net.ParseMAC(s.MAC)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.dialClient(escp.Config{MAC: hw, Username: s.Target.Username, Password: s.Target.Password})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if on, err := c.LED(context.Background()); err != nil || !on {
		t.Errorf("LED through a client that only knows the MAC = %t, %v", on, err)
	}
	// Nobody else has any other MAC.
	if _, err := s.dialClient(escp.Config{MAC: net.HardwareAddr{2, 0, 0, 0, 0, 1}}); !errors.Is(err, escp.ErrNotFound) {
		t.Errorf("a MAC the fake does not have: err = %v, want escp.ErrNotFound", err)
	}
}

func TestProviderBlockByMAC(t *testing.T) {
	s := Open(t, noEnv)
	only := s.ProviderHCLByMAC("", s.MAC)
	if !strings.Contains(only, `mac      = "`+s.MAC+`"`) || strings.Contains(only, "host") {
		t.Errorf("a block by MAC alone must name no host: %q", only)
	}
	both := s.ProviderHCLByMAC("192.0.2.9", s.MAC)
	if !strings.Contains(both, `host     = "192.0.2.9"`) || !strings.Contains(both, `mac      = "`+s.MAC+`"`) {
		t.Errorf("a block with both: %q", both)
	}
}
