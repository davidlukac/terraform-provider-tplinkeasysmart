package provider

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const (
	verifiedFirmware   = "1.0.0 Build 20230218 Rel.50633"
	newerFirmware      = "1.0.0 Build 20250710 Rel.71093"
	unverifiedFirmware = "1.0.0 Build 20990101 Rel.1"
)

func firmwareFake(version string) *fakeClient {
	f := &fakeClient{led: true}
	f.setFirmware(version)
	return f
}

// Every method that changes the switch must go through the firmware guard. The
// test finds the write methods by name on the SwitchClient interface, so a new
// Set* or Delete* method that the guard does not override makes it fail.
func TestFirmwareGuardCoversEveryWriteMethod(t *testing.T) {
	ifaceType := reflect.TypeOf((*SwitchClient)(nil)).Elem()
	checked := 0
	for i := 0; i < ifaceType.NumMethod(); i++ {
		m := ifaceType.Method(i)
		if !strings.HasPrefix(m.Name, "Set") && !strings.HasPrefix(m.Name, "Delete") {
			continue
		}
		checked++
		t.Run(m.Name, func(t *testing.T) {
			inner := newFaulty(firmwareFake(unverifiedFirmware))
			guard := newFirmwareGuard(inner, false)
			args := []reflect.Value{reflect.ValueOf(context.Background())}
			for a := 1; a < m.Type.NumIn(); a++ { // the interface method type has no receiver
				args = append(args, reflect.Zero(m.Type.In(a)))
			}
			out := reflect.ValueOf(guard).MethodByName(m.Name).Call(args)
			err, _ := out[len(out)-1].Interface().(error)
			if err == nil || !strings.Contains(err.Error(), "refusing to write") {
				t.Fatalf("%s on an unverified firmware returned %v, want a refusal", m.Name, err)
			}
			if inner.calls[m.Name] != 0 {
				t.Fatalf("%s reached the switch despite the refusal", m.Name)
			}
		})
	}
	if checked < 18 {
		t.Fatalf("found only %d write methods; the interface scan is broken", checked)
	}
}

func TestFirmwareGuardAllowsVerifiedBuildsAndReadsTheFirmwareOnce(t *testing.T) {
	for _, version := range []string{verifiedFirmware, newerFirmware} {
		inner := newFaulty(firmwareFake(version))
		guard := newFirmwareGuard(inner, false)
		for i := 0; i < 3; i++ {
			if err := guard.SetLED(context.Background(), false); err != nil {
				t.Fatalf("%s: %v", version, err)
			}
		}
		if inner.calls["SetLED"] != 3 || inner.calls["Info"] != 1 {
			t.Fatalf("%s: want 3 writes after one firmware read, got %v", version, inner.calls)
		}
	}
}

func TestFirmwareGuardRefusesAnUnverifiedBuildButNotReads(t *testing.T) {
	inner := newFaulty(firmwareFake(unverifiedFirmware))
	guard := newFirmwareGuard(inner, false)
	err := guard.SetLED(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "build 20990101 has not been verified") || !strings.Contains(err.Error(), "allow_unverified_firmware") {
		t.Fatalf("the refusal must name the build and the opt-in: %v", err)
	}
	if _, err := guard.Ports(context.Background()); err != nil {
		t.Fatalf("reads must work on any firmware: %v", err)
	}
	if _, err := guard.GetLED(context.Background()); err != nil {
		t.Fatalf("reads must work on any firmware: %v", err)
	}
	// the verdict is not cached: a switch that later reports a verified build is accepted
	inner.SwitchClient.(*fakeClient).setFirmware(verifiedFirmware)
	if err := guard.SetLED(context.Background(), false); err != nil {
		t.Fatalf("a refusal must not stick: %v", err)
	}
}

func TestFirmwareGuardRefusesWhenTheFirmwareCannotBeRead(t *testing.T) {
	inner := newFaulty(firmwareFake(verifiedFirmware))
	inner.failAlways("Info")
	guard := newFirmwareGuard(inner, false)
	err := guard.SetLED(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "cannot read the firmware version") {
		t.Fatalf("got %v", err)
	}
	if inner.calls["SetLED"] != 0 {
		t.Fatal("no write without a firmware check")
	}
	inner.heal()
	if err := guard.SetLED(context.Background(), false); err != nil {
		t.Fatalf("the check is retried once the switch answers: %v", err)
	}
}

func TestFirmwareGuardRefusesAFirmwareWithoutABuildDate(t *testing.T) {
	guard := newFirmwareGuard(firmwareFake("custom"), false)
	if err := guard.SetLED(context.Background(), true); err == nil || !strings.Contains(err.Error(), "has no build date") {
		t.Fatalf("got %v", err)
	}
}

func TestFirmwareGuardOptInReturnsTheClientUnchanged(t *testing.T) {
	inner := firmwareFake(unverifiedFirmware)
	if got := newFirmwareGuard(inner, true); got != SwitchClient(inner) {
		t.Fatalf("with the opt-in there is nothing to guard, got %T", got)
	}
}

const firmwareProvider = `
provider "tplinkeasysmart" {
  host     = "192.0.2.120"
  username = "admin"
  password = "x"
%s}
`

func firmwareLEDConfig(providerExtra string) string {
	return strings.Replace(firmwareProvider, "%s", providerExtra, 1) + `
data "tplinkeasysmart_system_info" "this" {}
resource "tplinkeasysmart_led" "this" {
  enabled = false
}
`
}

func TestAccUnverifiedFirmwareRefusesWritesButReadsWork(t *testing.T) {
	f := firmwareFake(unverifiedFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: firmwareLEDConfig(""), ExpectError: regexp.MustCompile(`refusing to write: firmware build 20990101 has not been verified`)},
		},
	})
	if f.led != true {
		t.Fatal("the LED must not have been touched")
	}
	// the data source reads fine on the same firmware
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: firmwareProviderOnly() + "\ndata \"tplinkeasysmart_system_info\" \"this\" {}\n",
			Check:  resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.this", "firmware_version", unverifiedFirmware),
		}},
	})
}

func firmwareProviderOnly() string { return strings.Replace(firmwareProvider, "%s", "", 1) }

func TestAccAllowUnverifiedFirmwareLiftsTheRefusal(t *testing.T) {
	f := firmwareFake(unverifiedFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{
			Config: firmwareLEDConfig("  allow_unverified_firmware = true\n"),
			Check:  func(*terraformState) error { return expectLED(f, false) },
		}},
	})
}

func TestAccVerifiedFirmwareNeedsNoOptIn(t *testing.T) {
	f := firmwareFake(newerFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps:                    []resource.TestStep{{Config: firmwareLEDConfig(""), Check: func(*terraformState) error { return expectLED(f, false) }}},
	})
}

func TestAccAllowUnverifiedFirmwareCanBeSetPerSwitch(t *testing.T) {
	clearProviderEnv(t)
	a, b := firmwareFake(unverifiedFirmware), firmwareFake(unverifiedFirmware)
	fakes := map[string]SwitchClient{"10.0.0.1": a, "10.0.0.2": b}
	cfg := func(provider string) string {
		return provider + `
resource "tplinkeasysmart_led" "a" {
  switch  = "sw01"
  enabled = false
}
`
	}
	const opts = `
provider "tplinkeasysmart" {
  allow_unverified_firmware = %s
  switches = {
    sw01 = { host = "10.0.0.1", username = "admin", password = "one"%s }
    sw02 = { host = "10.0.0.2", username = "admin", password = "two" }
  }
}
`
	build := func(provider, perSwitch string) string {
		return cfg(strings.Replace(strings.Replace(opts, "%s", provider, 1), "%s", perSwitch, 1))
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, nil),
		Steps: []resource.TestStep{
			// the provider-wide default refuses; the switch entry overrides it and wins
			{Config: build("false", ""), ExpectError: regexp.MustCompile(`has not been verified`)},
			{Config: build("false", ", allow_unverified_firmware = true"), Check: func(*terraformState) error { return expectLED(a, false) }},
		},
	})
	// the other direction: allowed everywhere, refused for one switch
	c := firmwareFake(unverifiedFirmware)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(map[string]SwitchClient{"10.0.0.1": c, "10.0.0.2": b}, nil),
		Steps: []resource.TestStep{
			{Config: build("true", ", allow_unverified_firmware = false"), ExpectError: regexp.MustCompile(`has not been verified`)},
		},
	})
	if !c.led {
		t.Fatal("the refused switch must not be written")
	}
}
