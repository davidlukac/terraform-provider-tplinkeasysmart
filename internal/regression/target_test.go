package regression

import (
	"fmt"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

func sw03Env() map[string]string {
	return map[string]string{
		EnvTarget: TargetSW03, envHost: "192.0.2.13", envUsername: "admin", envPassword: "pw",
		envTestPorts: "2, 3,4,5", envProtectedPorts: "1,6,7,8", envReadOnlyHosts: "192.0.2.11, 192.0.2.99",
	}
}

func TestLoadTargetFakeIsTheDefault(t *testing.T) {
	for _, env := range []map[string]string{{}, {EnvTarget: TargetFake}} {
		got, err := LoadTarget(env)
		if err != nil || got.Name != TargetFake || got.Real() || got.Host != "127.0.0.1" {
			t.Errorf("LoadTarget(%v) = %+v, %v", env, got, err)
		}
	}
}

func TestLoadTargetSW03(t *testing.T) {
	got, err := LoadTarget(sw03Env())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Real() || got.Host != "192.0.2.13" || got.Username != "admin" || got.Password != "pw" {
		t.Errorf("target = %+v", got)
	}
	if want := []escp.PortID{2, 3, 4, 5}; len(got.TestPorts) != 4 || got.TestPorts[0] != want[0] || got.TestPorts[3] != want[3] {
		t.Errorf("test ports = %v, want %v", got.TestPorts, want)
	}
	if len(got.ProtectedPorts) != 4 || got.ProtectedPorts[0] != 1 {
		t.Errorf("protected ports = %v", got.ProtectedPorts)
	}
}

func TestLoadTargetRefusals(t *testing.T) {
	with := func(k, v string) map[string]string { e := sw03Env(); e[k] = v; return e }
	without := func(k string) map[string]string { e := sw03Env(); delete(e, k); return e }
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"switch 01 by name", map[string]string{EnvTarget: "sw01"}, "never a target"},
		{"unknown name", map[string]string{EnvTarget: "sw07"}, "unknown target"},
		{"switch 01 by address", with(envHost, "192.0.2.11"), "read-only switch"},
		{"no host", without(envHost), envHost},
		{"no username", without(envUsername), envUsername},
		{"no password", without(envPassword), envPassword},
		{"no test port", without(envTestPorts), "no test port"},
		{"bad test port", with(envTestPorts, "2,x"), "not a port number"},
		{"port zero", with(envTestPorts, "0"), "not a port number"},
		{"bad protected port", with(envProtectedPorts, "-1"), "not a port number"},
		{"test port is protected", with(envTestPorts, "2,6"), "both a test port and a protected port"},
		{"disrupt the uplink", with(envDisruptPort, "1"), "not a device port"},
		{"disrupt a test port", with(envDisruptPort, "3"), "not a device port"},
		{"disrupt two ports", with(envDisruptPort, "6,7"), "not one port number"},
		{"disrupt a non-number", with(envDisruptPort, "x"), "not one port number"},
		{"disruptive uplink", with(envDisruptivePorts, "1,7"), "not a device port"},
		{"disruptive test port", with(envDisruptivePorts, "7,3"), "not a device port"},
		{"disruptive not numbers", with(envDisruptivePorts, "x"), "not a list of port numbers"},
		{"move without an address", with(EnvAllowMove, "1"), "not an IPv4 address"},
		{"move to a name", func() map[string]string { e := with(EnvAllowMove, "1"); e[envMoveIP] = "spare"; return e }(), "not an IPv4 address"},
		{"move to its own address", func() map[string]string { e := with(EnvAllowMove, "1"); e[envMoveIP] = "192.0.2.13"; return e }(), "own address"},
		{"move to switch 01", func() map[string]string { e := with(EnvAllowMove, "1"); e[envMoveIP] = "192.0.2.99"; return e }(), "read-only switch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadTarget(tc.env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestParseEnvFile(t *testing.T) {
	got, err := ParseEnvFile(strings.NewReader("# comment\n\nA=1\nexport B=\"two words\"\nC='x'\n D = y \n"))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"A": "1", "B": "two words", "C": "x", "D": "y"} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}
	if _, err := ParseEnvFile(strings.NewReader("ok=1\nsecret-without-equals\n")); err == nil ||
		!strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "secret-without") {
		t.Errorf("err = %v, want a message naming line 2 and not the content", err)
	}
	if _, err := ParseEnvFile(strings.NewReader("=v\n")); err == nil {
		t.Error("an empty key was accepted")
	}
}

func TestLoadTargetMove(t *testing.T) {
	e := sw03Env()
	e[envMoveIP] = "192.0.2.14"
	got, err := LoadTarget(e)
	if err != nil || got.MoveIP != "" {
		t.Fatalf("without the opt-in the spare address is ignored: %+v, %v", got, err)
	}
	e[EnvAllowMove] = "1"
	got, err = LoadTarget(e)
	if err != nil || got.MoveIP != "192.0.2.14" {
		t.Fatalf("with the opt-in: %+v, %v", got, err)
	}
}

func TestLoadTargetDHCP(t *testing.T) {
	e := sw03Env()
	if got, err := LoadTarget(e); err != nil || got.DHCP {
		t.Fatalf("without the opt-in: %+v, %v", got, err)
	}
	e[EnvAllowDHCP] = "1"
	if got, err := LoadTarget(e); err != nil || !got.DHCP {
		t.Fatalf("with the opt-in: %+v, %v", got, err)
	}
}

func TestLoadTargetDisruptPort(t *testing.T) {
	got, err := LoadTarget(sw03Env())
	if err != nil || got.DisruptPort != 0 {
		t.Fatalf("without the opt-in: %+v, %v", got, err)
	}
	e := sw03Env()
	e[envDisruptPort] = "8"
	got, err = LoadTarget(e)
	if err != nil || got.DisruptPort != 8 {
		t.Fatalf("with the opt-in: %+v, %v", got, err)
	}
}

func TestLoadTargetDisruptivePorts(t *testing.T) {
	got, err := LoadTarget(sw03Env())
	if err != nil || len(got.DisruptivePorts) != 0 {
		t.Fatalf("without the opt-in: %+v, %v", got, err)
	}
	e := sw03Env()
	e[envDisruptivePorts] = "7, 8"
	got, err = LoadTarget(e)
	if err != nil || fmt.Sprint(got.DisruptivePorts) != "[7 8]" {
		t.Fatalf("with the opt-in: %+v, %v", got, err)
	}
	if fake, _ := LoadTarget(map[string]string{}); fmt.Sprint(fake.DisruptivePorts) != "[7 8]" {
		t.Fatalf("the fake always runs the disruptive scenarios, on its device ports 7 and 8: %v", fake.DisruptivePorts)
	}
}
