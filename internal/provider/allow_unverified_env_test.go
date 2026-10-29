package provider

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestEnvBoolOr(t *testing.T) {
	const env = "TPLINK_EASYSMART_TEST_BOOL"
	for _, c := range []struct {
		name    string
		v       types.Bool
		env     string
		want    bool
		wantErr string
	}{
		{name: "nothing set is false", v: types.BoolNull(), env: "", want: false},
		{name: "the configured true wins over a false variable", v: types.BoolValue(true), env: "false", want: true},
		{name: "the configured false wins over a true variable", v: types.BoolValue(false), env: "true", want: false},
		{name: "null falls back to the variable", v: types.BoolNull(), env: "true", want: true},
		{name: "unknown falls back to the variable", v: types.BoolUnknown(), env: "true", want: true},
		{name: "1 is true", v: types.BoolNull(), env: "1", want: true},
		{name: "TRUE is true", v: types.BoolNull(), env: "TRUE", want: true},
		{name: "0 is false", v: types.BoolNull(), env: "0", want: false},
		{name: "false is false", v: types.BoolNull(), env: "false", want: false},
		{name: "a typo is an error naming the variable and the value", v: types.BoolNull(), env: "yes", wantErr: env + ` is "yes"`},
		{name: "a typo is ignored when the argument is set", v: types.BoolValue(true), env: "yes", want: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(env, c.env)
			got, err := envBoolOr(c.v, env)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("got %t, %v; want %t", got, err, c.want)
			}
		})
	}
}

// The variable is the default of the provider's own switch; the same rules as for the argument apply.
func TestAccAllowUnverifiedFirmwareFromTheEnvironment(t *testing.T) {
	t.Run("true lifts the refusal", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv(envAllowUnverified, "true")
		f := firmwareFake(unverifiedFirmware)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: firmwareLEDConfig(""), Check: func(*terraformState) error { return expectLED(f, false) }}},
		})
	})
	t.Run("false, like nothing, keeps the refusal", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv(envAllowUnverified, "false")
		f := firmwareFake(unverifiedFirmware)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: firmwareLEDConfig(""), ExpectError: regexp.MustCompile(`has not been verified`)}},
		})
		if !f.led {
			t.Fatal("the LED must not have been touched")
		}
	})
	t.Run("the argument in the configuration wins", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv(envAllowUnverified, "true")
		f := firmwareFake(unverifiedFirmware)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: firmwareLEDConfig("  allow_unverified_firmware = false\n"), ExpectError: regexp.MustCompile(`has not been verified`)}},
		})
		if !f.led {
			t.Fatal("the LED must not have been touched")
		}
	})
	t.Run("a value that is not a boolean stops the run before any write", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv(envAllowUnverified, "yes")
		f := firmwareFake(unverifiedFirmware)
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(f),
			Steps:                    []resource.TestStep{{Config: firmwareLEDConfig(""), ExpectError: regexp.MustCompile(`(?s)Invalid allow_unverified_firmware.*TPLINK_EASYSMART_ALLOW_UNVERIFIED_FIRMWARE\s+is\s+"yes"`)}},
		})
		if !f.led {
			t.Fatal("the LED must not have been touched")
		}
	})
	t.Run("a switch entry still overrides it", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv(envAllowUnverified, "true")
		a := firmwareFake(unverifiedFirmware)
		cfg := `
provider "tplinkeasysmart" {
  switches = {
    sw01 = { host = "10.0.0.1", username = "admin", password = "one", allow_unverified_firmware = false }
  }
}
resource "tplinkeasysmart_led" "a" {
  switch  = "sw01"
  enabled = false
}
`
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: multiFactories(map[string]SwitchClient{"10.0.0.1": a}, nil),
			Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`has not been verified`)}},
		})
		if !a.led {
			t.Fatal("the LED must not have been touched")
		}
	})
}
