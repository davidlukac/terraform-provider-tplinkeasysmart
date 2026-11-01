package provider

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/safety"
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

// gatedClient stands in for what the real escp.Client does with the write gate
// that the provider puts into its configuration: it consults the gate before
// every write and sends nothing when the gate refuses. The guarantee that the
// real client does so for every write method is tested in package escp
// (TestWriteGateRefusalSendsNothingForAnyWriteMethod); this double lets the
// Terraform lifecycle tests exercise the provider's wiring and wording of it.
type gatedClient struct {
	SwitchClient
	gate escp.WriteGate
}

// gated returns c behind the gate of cfg, or c itself when cfg has no gate.
func gated(c SwitchClient, cfg escp.Config) SwitchClient {
	if cfg.WriteGate == nil {
		return c
	}
	return &gatedClient{SwitchClient: c, gate: cfg.WriteGate}
}

// allow consults the gate with no operation: the provider's firmware gate does not look at it.
func (g *gatedClient) allow(ctx context.Context) error {
	return g.gate(ctx, g.SwitchClient, escp.Operation{})
}

func (g *gatedClient) SetLED(ctx context.Context, on bool) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetLED(ctx, on)
}

func (g *gatedClient) SetPort(ctx context.Context, cfg escp.PortConfig) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPort(ctx, cfg)
}

func (g *gatedClient) SetLAG(ctx context.Context, lag escp.LAG) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetLAG(ctx, lag)
}

func (g *gatedClient) SetIGMP(ctx context.Context, cfg escp.IGMPConfig) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetIGMP(ctx, cfg)
}

func (g *gatedClient) SetLoopPrevention(ctx context.Context, on bool) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetLoopPrevention(ctx, on)
}

func (g *gatedClient) SetPortMirror(ctx context.Context, m escp.Mirror) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortMirror(ctx, m)
}

func (g *gatedClient) SetPortBasedVLAN(ctx context.Context, group escp.PortVLAN) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortBasedVLAN(ctx, group)
}

func (g *gatedClient) SetVLANMode(ctx context.Context, s escp.VLANModeSetting) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetVLANMode(ctx, s)
}

func (g *gatedClient) SetDot1QVLAN(ctx context.Context, v escp.Dot1QVLAN) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetDot1QVLAN(ctx, v)
}

func (g *gatedClient) DeleteDot1QVLAN(ctx context.Context, id escp.VLANID) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.DeleteDot1QVLAN(ctx, id)
}

func (g *gatedClient) SetPVIDs(ctx context.Context, pvids map[escp.PortID]escp.VLANID) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPVIDs(ctx, pvids)
}

func (g *gatedClient) SetQoSMode(ctx context.Context, mode escp.QoSMode) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetQoSMode(ctx, mode)
}

func (g *gatedClient) SetPortPriorities(ctx context.Context, changes map[escp.PortID]escp.Priority) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetPortPriorities(ctx, changes)
}

func (g *gatedClient) SetBandwidth(ctx context.Context, limit escp.PortBandwidth) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetBandwidth(ctx, limit)
}

func (g *gatedClient) SetStormControl(ctx context.Context, cfg escp.StormConfig) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetStormControl(ctx, cfg)
}

func (g *gatedClient) SetHostname(ctx context.Context, name escp.Hostname) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetHostname(ctx, name)
}

func (g *gatedClient) EnableDHCP(ctx context.Context) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.EnableDHCP(ctx)
}

func (g *gatedClient) SetIPConfig(ctx context.Context, cfg escp.IPConfig) error {
	if err := g.allow(ctx); err != nil {
		return err
	}
	return g.SwitchClient.SetIPConfig(ctx, cfg)
}

// The double must gate every write method of the interface, or the lifecycle
// tests below would silently let a write through. The test finds the write
// methods by name, so a new Set* or Delete* method that the double does not
// override makes it fail.
func TestGatedTestClientCoversEveryWriteMethod(t *testing.T) {
	ifaceType := reflect.TypeOf((*SwitchClient)(nil)).Elem()
	checked := 0
	for i := 0; i < ifaceType.NumMethod(); i++ {
		m := ifaceType.Method(i)
		if !strings.HasPrefix(m.Name, "Set") && !strings.HasPrefix(m.Name, "Delete") && !strings.HasPrefix(m.Name, "Enable") {
			continue
		}
		checked++
		t.Run(m.Name, func(t *testing.T) {
			inner := newFaulty(firmwareFake(unverifiedFirmware))
			client := gated(inner, escp.Config{WriteGate: firmwareWriteGate(false)})
			args := []reflect.Value{reflect.ValueOf(context.Background())}
			for a := 1; a < m.Type.NumIn(); a++ { // the interface method type has no receiver
				args = append(args, reflect.Zero(m.Type.In(a)))
			}
			out := reflect.ValueOf(client).MethodByName(m.Name).Call(args)
			err, _ := out[len(out)-1].Interface().(error)
			if err == nil || !strings.Contains(err.Error(), "refusing to write") {
				t.Fatalf("%s on an unverified firmware returned %v, want a refusal", m.Name, err)
			}
			if inner.calls[m.Name] != 0 {
				t.Fatalf("%s reached the switch despite the refusal", m.Name)
			}
		})
	}
	if checked < 16 { // a floor against a broken scan, not a count of the writers
		t.Fatalf("found only %d write methods; the interface scan is broken", checked)
	}
}

func TestFirmwareGateAllowsVerifiedBuildsAndReadsTheFirmwareOnce(t *testing.T) {
	for _, version := range []string{verifiedFirmware, newerFirmware} {
		inner := newFaulty(firmwareFake(version))
		client := gated(inner, escp.Config{WriteGate: firmwareWriteGate(false)})
		for i := 0; i < 3; i++ {
			if err := client.SetLED(context.Background(), false); err != nil {
				t.Fatalf("%s: %v", version, err)
			}
		}
		if inner.calls["SetLED"] != 3 || inner.calls["Info"] != 1 {
			t.Fatalf("%s: want 3 writes after one firmware read, got %v", version, inner.calls)
		}
	}
}

func TestFirmwareGateRefusesAnUnverifiedBuildButNotReads(t *testing.T) {
	inner := newFaulty(firmwareFake(unverifiedFirmware))
	client := gated(inner, escp.Config{WriteGate: firmwareWriteGate(false)})
	err := client.SetLED(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "build 20990101 has not been verified") || !strings.Contains(err.Error(), "allow_unverified_firmware") {
		t.Fatalf("the refusal must name the build and the opt-in: %v", err)
	}
	if _, err := client.Ports(context.Background()); err != nil {
		t.Fatalf("reads must work on any firmware: %v", err)
	}
	if _, err := client.LED(context.Background()); err != nil {
		t.Fatalf("reads must work on any firmware: %v", err)
	}
	// the verdict is not cached: a switch that later reports a verified build is accepted
	inner.SwitchClient.(*fakeClient).setFirmware(verifiedFirmware)
	if err := client.SetLED(context.Background(), false); err != nil {
		t.Fatalf("a refusal must not stick: %v", err)
	}
}

func TestFirmwareGateRefusesWhenTheFirmwareCannotBeRead(t *testing.T) {
	inner := newFaulty(firmwareFake(verifiedFirmware))
	inner.failAlways("Info")
	client := gated(inner, escp.Config{WriteGate: firmwareWriteGate(false)})
	err := client.SetLED(context.Background(), false)
	if err == nil || !strings.Contains(err.Error(), "refusing to write: cannot read the firmware version") {
		t.Fatalf("got %v", err)
	}
	if inner.calls["SetLED"] != 0 {
		t.Fatal("no write without a firmware check")
	}
	inner.heal()
	if err := client.SetLED(context.Background(), false); err != nil {
		t.Fatalf("the check is retried once the switch answers: %v", err)
	}
}

func TestFirmwareGateRefusesAFirmwareWithoutABuildDate(t *testing.T) {
	client := gated(firmwareFake("custom"), escp.Config{WriteGate: firmwareWriteGate(false)})
	err := client.SetLED(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "has no build date") || !strings.Contains(err.Error(), "allow_unverified_firmware") {
		t.Fatalf("got %v", err)
	}
}

func TestFirmwareGateOptInHasNoGate(t *testing.T) {
	if firmwareWriteGate(true) != nil {
		t.Fatal("with the opt-in there is nothing to gate")
	}
}

// The wording of a refusal is the provider's; an error the safety package does
// not describe is passed on with the same prefix rather than lost.
func TestWriteRefusalKeepsAnUnknownErrorAndItsCause(t *testing.T) {
	cause := errors.New("boom")
	if err := writeRefusal(cause); !errors.Is(err, cause) || !strings.HasPrefix(err.Error(), "refusing to write: ") {
		t.Fatalf("got %v", err)
	}
	unreadable := &safety.FirmwareError{Reason: safety.FirmwareUnreadable, Err: cause}
	if err := writeRefusal(unreadable); !errors.Is(err, cause) || strings.Contains(err.Error(), "allow_unverified_firmware") {
		t.Fatalf("an unreadable firmware is reported as it is, with its cause: %v", err)
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
