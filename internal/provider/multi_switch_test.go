package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// ---------------------------------------------------------------- the registry

func TestRegistryResolvesSwitchesByName(t *testing.T) {
	def, a, b := &fakeClient{}, &fakeClient{}, &fakeClient{}
	cases := []struct {
		name    string
		reg     *switchRegistry
		ask     string
		want    SwitchClient
		wantErr string
	}{
		{"named switch", &switchRegistry{def: def, named: map[string]SwitchClient{"sw01": a, "sw02": b}}, "sw02", b, ""},
		{"omitted name uses the default", &switchRegistry{def: def, named: map[string]SwitchClient{"sw01": a}}, "", def, ""},
		{"omitted name uses the only switch when there is no default", &switchRegistry{named: map[string]SwitchClient{"sw01": a}}, "", a, ""},
		{"omitted name is ambiguous with several switches and no default", &switchRegistry{named: map[string]SwitchClient{"sw01": a, "sw02": b}}, "", nil,
			"several switches are configured and none is the default: set `switch` to one of sw01, sw02"},
		{"unknown name lists the configured ones", &switchRegistry{named: map[string]SwitchClient{"sw02": b, "sw01": a}}, "nope", nil, `unknown switch "nope" (configured: sw01, sw02)`},
	}
	for _, tc := range cases {
		got, err := tc.reg.Client(tc.ask)
		if tc.wantErr != "" {
			if err == nil || err.Error() != tc.wantErr {
				t.Errorf("%s: error %v, want %q", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %v, %v", tc.name, got, err)
		}
	}
}

func TestClientForNeedsAConfiguredProviderAndACapableClient(t *testing.T) {
	if _, err := clientFor[ledClient](nil, types.StringNull()); err == nil {
		t.Fatal("an unconfigured provider must be reported, not dereferenced")
	}
	reg := &switchRegistry{def: &fakeClient{}}
	type needsMore interface{ MethodNoSwitchClientHas() } // a consumer interface that is not a subset of the client API
	if _, err := clientFor[needsMore](reg, types.StringNull()); err == nil {
		t.Fatal("a client that lacks the needed methods must be reported")
	}
	if c, err := clientFor[ledClient](reg, types.StringNull()); err != nil || c == nil {
		t.Fatalf("a capable client must be returned, got %v %v", c, err)
	}
	if _, err := clientFor[ledClient](reg, types.StringValue("x")); err == nil {
		t.Fatal("an unknown switch name must be reported")
	}
}

func TestSourceFromRefusesForeignProviderData(t *testing.T) {
	var diags diag.Diagnostics
	if sourceFrom(nil, &diags) != nil || diags.HasError() {
		t.Fatal("no provider data means the provider is not configured yet; that is not an error")
	}
	reg := &switchRegistry{}
	if sourceFrom(reg, &diags) != switchSource(reg) || diags.HasError() {
		t.Fatal("a registry must be passed through")
	}
	if sourceFrom("not a registry", &diags) != nil || !diags.HasError() {
		t.Fatal("foreign data must produce a diagnostic")
	}
}

func TestBindSwitchReportsAndReturnsNilOnFailure(t *testing.T) {
	type holder struct {
		c  ledClient
		sw types.String
	}
	bind := func(h *holder, c ledClient, sw types.String) { h.c, h.sw = c, sw }
	base := &holder{}
	var summary string
	add := func(s, _ string) { summary = s }
	if got := bindSwitch(add, &switchRegistry{named: map[string]SwitchClient{"a": &fakeClient{}}}, types.StringValue("zzz"), base, bind); got != nil || summary != summaryUnknownSwitch {
		t.Fatalf("unknown switch: got %v summary %q", got, summary)
	}
	got := bindSwitch(add, &switchRegistry{named: map[string]SwitchClient{"a": &fakeClient{}}}, types.StringValue("a"), base, bind)
	if got == nil || got.c == nil || got.sw.ValueString() != "a" {
		t.Fatalf("bound copy: %+v", got)
	}
	if base.c != nil {
		t.Fatal("the shared instance must stay untouched: it serves other switches")
	}
}

func TestIdsAreQualifiedByTheSwitch(t *testing.T) {
	for _, tc := range []struct {
		sw   types.String
		want string
	}{{types.StringNull(), "led"}, {types.StringUnknown(), "led"}, {types.StringValue(""), "led"}, {types.StringValue("sw02"), "sw02:led"}} {
		if got := qualifiedID(tc.sw, "led").ValueString(); got != tc.want {
			t.Errorf("qualifiedID(%v) = %q, want %q", tc.sw, got, tc.want)
		}
	}
	for _, tc := range []struct{ in, sw, rest string }{{"7", "", "7"}, {"sw02:7", "sw02", "7"}, {"sw02:led", "sw02", "led"}, {":x", "", "x"}} {
		if sw, rest := splitImportID(tc.in); sw != tc.sw || rest != tc.rest {
			t.Errorf("splitImportID(%q) = %q,%q want %q,%q", tc.in, sw, rest, tc.sw, tc.rest)
		}
	}
}

func TestCheckSwitchAndKeepSwitchAtTheEdges(t *testing.T) {
	ctx := context.Background()
	var sch = func() tfsdk.Plan {
		var s fwresource.SchemaResponse
		NewLEDResource().Schema(ctx, fwresource.SchemaRequest{}, &s)
		return tfsdk.Plan{Schema: s.Schema, Raw: tftypes.NewValue(s.Schema.Type().TerraformType(ctx), nil)}
	}()
	var diags diag.Diagnostics
	checkSwitch(ctx, nil, sch, &diags)               // provider not configured yet
	checkSwitch(ctx, &switchRegistry{}, sch, &diags) // destroy plan: nothing to resolve
	if diags.HasError() {
		t.Fatalf("neither case may report anything: %v", diags)
	}
	// keepSwitch must not touch a failed read or a removed object
	state := tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(sch.Schema.Type().TerraformType(ctx), nil)}
	keepSwitch(ctx, types.StringValue("x"), &state, &diags)
	failed := diag.Diagnostics{diag.NewErrorDiagnostic("boom", "")}
	keepSwitch(ctx, types.StringValue("x"), &state, &failed)
	if len(failed) != 1 || diags.HasError() {
		t.Fatal("keepSwitch must leave removed or failed state alone")
	}
}

// ------------------------------------------------------------ provider options

type configLog struct {
	mu   sync.Mutex
	cfgs []escp.Config
}

func (l *configLog) byHost() map[string]escp.Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	m := map[string]escp.Config{}
	for _, c := range l.cfgs {
		m[c.Host] = c
	}
	return m
}

// multiFactories builds the provider with one fake switch per host and records
// the configuration each client was created with.
func multiFactories(fakes map[string]SwitchClient, log *configLog) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{
			version: "test",
			newClient: func(c escp.Config) (SwitchClient, error) {
				if log != nil {
					log.mu.Lock()
					log.cfgs = append(log.cfgs, c)
					log.mu.Unlock()
				}
				f, ok := fakes[c.Host]
				if !ok {
					return nil, fmt.Errorf("no fake for host %s", c.Host)
				}
				return gated(f, c), nil
			},
		}),
	}
}

const twoSwitches = `
provider "tplinkeasysmart" {
  switches = {
    sw01 = { host = "10.0.0.1", username = "admin", password = "one" }
    sw02 = { host = "10.0.0.2", username = "admin", password = "two", timeout_seconds = 9 }
  }
}
`

func twoFakes() (a, b *fakeClient, fakes map[string]SwitchClient) {
	a, b = &fakeClient{led: true}, &fakeClient{led: false}
	return a, b, map[string]SwitchClient{"10.0.0.1": a, "10.0.0.2": b}
}

// The plugin-testing harness cannot reconcile string-keyed for_each addresses
// during its own cleanup, so the per-switch instances are expressed with count
// and a list of names here; the provider only ever sees the `switch` value.
func TestSwitchesMapManagesSeveralSwitchesWithOneProvider(t *testing.T) {
	clearProviderEnv(t)
	a, b, fakes := twoFakes()
	log := &configLog{}
	cfg := twoSwitches + `
locals {
  names = ["sw01", "sw02"]
}
resource "tplinkeasysmart_led" "this" {
  count   = 2
  switch  = local.names[count.index]
  enabled = count.index == 0
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, log),
		Steps: []resource.TestStep{
			{Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("tplinkeasysmart_led.this.0", "id", "sw01:led"),
					resource.TestCheckResourceAttr("tplinkeasysmart_led.this.1", "id", "sw02:led"),
					resource.TestCheckResourceAttr("tplinkeasysmart_led.this.1", "switch", "sw02"),
					func(*terraformState) error {
						if !a.led || b.led {
							return fmt.Errorf("each switch must get its own value: sw01 led=%v sw02 led=%v", a.led, b.led)
						}
						return nil
					})},
			// a second plan must be empty: the switch argument survives every refresh
			{Config: cfg, PlanOnly: true},
		},
	})
	got := log.byHost()
	if got["10.0.0.1"].Password != "one" || got["10.0.0.2"].Password != "two" || got["10.0.0.2"].Timeout != 9*time.Second || got["10.0.0.1"].Timeout != defaultTimeout {
		t.Fatalf("every switch must be created with its own settings: %+v", got)
	}
}

func TestSwitchesAndTheProviderOwnSwitchWorkTogether(t *testing.T) {
	clearProviderEnv(t)
	own, other := &fakeClient{led: true}, &fakeClient{led: false}
	cfg := `
provider "tplinkeasysmart" {
  host     = "10.0.0.9"
  username = "admin"
  password = "own"
  switches = {
    sw02 = { host = "10.0.0.2", username = "admin", password = "two" }
  }
}
resource "tplinkeasysmart_led" "default" {
  enabled = false
}
resource "tplinkeasysmart_led" "named" {
  switch  = "sw02"
  enabled = true
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(map[string]SwitchClient{"10.0.0.9": own, "10.0.0.2": other}, nil),
		Steps: []resource.TestStep{{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("tplinkeasysmart_led.default", "id", "led"), // unchanged for the provider's own switch
			resource.TestCheckResourceAttr("tplinkeasysmart_led.named", "id", "sw02:led"),
			func(*terraformState) error {
				if own.led || !other.led {
					return fmt.Errorf("the own switch must get false and sw02 true, got %v / %v", own.led, other.led)
				}
				return nil
			},
		)}},
	})
}

func TestSwitchSelectionErrorsAreReportedWhilePlanning(t *testing.T) {
	clearProviderEnv(t)
	cases := []struct{ name, body, want string }{
		{"several switches and no switch argument", `resource "tplinkeasysmart_led" "x" {
  enabled = true
}`, `(?s)set\s+.switch.\s+to\s+one\s+of\s+sw01,\s+sw02`},
		{"unknown switch name", `resource "tplinkeasysmart_led" "x" {
  switch  = "nope"
  enabled = true
}`, `unknown switch "nope"`},
		{"invalid switch name", `resource "tplinkeasysmart_led" "x" {
  switch  = "bad name"
  enabled = true
}`, `(?s)letters,\s+digits,\s+hyphens`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, b, fakes := twoFakes()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: multiFactories(fakes, nil),
				Steps:                    []resource.TestStep{{Config: twoSwitches + tc.body, PlanOnly: true, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if a.sets != 0 || b.sets != 0 {
				t.Fatal("nothing may be written")
			}
		})
	}
	t.Run("data source without a switch argument", func(t *testing.T) {
		_, _, fakes := twoFakes()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: multiFactories(fakes, nil),
			Steps:                    []resource.TestStep{{Config: twoSwitches + `data "tplinkeasysmart_ports" "x" {}`, ExpectError: regexp.MustCompile(`(?s)set\s+.switch.\s+to\s+one\s+of`)}},
		})
	})
}

func TestChangingTheSwitchReplacesTheResource(t *testing.T) {
	clearProviderEnv(t)
	a, b, fakes := twoFakes()
	body := func(sw string) string {
		return twoSwitches + fmt.Sprintf("resource \"tplinkeasysmart_led\" \"x\" {\n  switch  = %q\n  enabled = true\n}\n", sw)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, nil),
		Steps: []resource.TestStep{
			{Config: body("sw01")},
			{Config: body("sw02"), ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
				plancheck.ExpectResourceAction("tplinkeasysmart_led.x", plancheck.ResourceActionReplace)}}},
		},
	})
	if !a.led || !b.led {
		t.Fatalf("both switches must have been written once the resource moved: %v %v", a.led, b.led)
	}
}

func TestDataSourcesReadTheSwitchTheyAreToldTo(t *testing.T) {
	clearProviderEnv(t)
	a, b := newHostnameFake("sw-alpha"), newHostnameFake("sw-beta")
	fakes := map[string]SwitchClient{"10.0.0.1": a, "10.0.0.2": b}
	cfg := twoSwitches + `
locals {
  names = ["sw01", "sw02"]
}
data "tplinkeasysmart_system_info" "x" {
  count  = 2
  switch = local.names[count.index]
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, nil),
		Steps: []resource.TestStep{{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.x.0", "hostname", "sw-alpha"),
			resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.x.1", "hostname", "sw-beta"),
			resource.TestCheckResourceAttr("data.tplinkeasysmart_system_info.x.1", "switch", "sw02"),
		)}},
	})
}

func TestGuardsUseThePortsOfTheSelectedSwitch(t *testing.T) {
	clearProviderEnv(t)
	// port 7 is free on sw01 but linked on sw02
	a := &fakeClient{ports: []escp.Port{{ID: 7, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"}}}
	b := &fakeClient{ports: []escp.Port{{ID: 7, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"}}}
	fakes := map[string]SwitchClient{"10.0.0.1": a, "10.0.0.2": b}
	free := twoSwitches + "resource \"tplinkeasysmart_port\" \"p\" {\n  switch = \"sw01\"\n  port = 7\n  flow_control = true\n}\n"
	busy := twoSwitches + "resource \"tplinkeasysmart_port\" \"p\" {\n  switch = \"sw02\"\n  port = 7\n  flow_control = true\n}\n"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, nil),
		Steps: []resource.TestStep{
			{Config: busy, ExpectError: regexp.MustCompile(`has a link up`)},
			{Config: free, Check: resource.TestCheckResourceAttr("tplinkeasysmart_port.p", "flow_control", "true")},
		},
	})
	if b.sets != 0 || a.sets != 1 {
		t.Fatalf("only the free port on sw01 may be written: sw01 sets=%d sw02 sets=%d", a.sets, b.sets)
	}
}

func TestSwitchesConfigurationIsValidated(t *testing.T) {
	clearProviderEnv(t)
	_, _, fakes := twoFakes()
	cases := []struct{ name, cfg, want string }{
		{"the same host twice", `provider "tplinkeasysmart" {
  switches = {
    a = { host = "10.0.0.1", username = "u", password = "p" }
    b = { host = "10.0.0.1", username = "u", password = "p" }
  }
}`, `Same switch configured twice`},
		{"the own switch duplicated in the map", `provider "tplinkeasysmart" {
  host     = "10.0.0.1"
  username = "u"
  password = "p"
  switches = { a = { host = "10.0.0.1", username = "u", password = "p" } }
}`, `Same switch configured twice`},
		{"a switch name with a separator", `provider "tplinkeasysmart" {
  switches = { "a:b" = { host = "10.0.0.1", username = "u", password = "p" } }
}`, `(?s)letters,\s+digits,\s+hyphens`},
		{"an entry without credentials", `provider "tplinkeasysmart" {
  switches = { a = { host = "10.0.0.1" } }
}`, `username`},
		{"a timeout below one second", `provider "tplinkeasysmart" {
  switches = { a = { host = "10.0.0.1", username = "u", password = "p", timeout_seconds = 0 } }
}`, `at least 1`},
		{"a host that is only known later", `resource "terraform_data" "h" {
  input = "10.0.0.1"
}
provider "tplinkeasysmart" {
  switches = { a = { host = terraform_data.h.output, username = "u", password = "p" } }
}`, `Unknown switch host`},
		{"a client that cannot be created", `provider "tplinkeasysmart" {
  switches = { a = { host = "10.9.9.9", username = "u", password = "p" } }
}`, `Cannot configure switch client`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: multiFactories(fakes, nil),
				Steps:                    []resource.TestStep{{Config: tc.cfg + "\ndata \"tplinkeasysmart_ports\" \"x\" {\n  switch = \"a\"\n}\n", ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}

func TestSortedKeysIsStable(t *testing.T) {
	got := sortedKeys(map[string]int{"b": 1, "c": 2, "a": 3})
	if !sort.StringsAreSorted(got) || len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

// A named switch's object is imported with the switch name in front of the id,
// and the resulting state keeps both the prefixed id and the switch argument.
func TestImportIdsCarryTheSwitchName(t *testing.T) {
	clearProviderEnv(t)
	a, b, fakes := twoFakes()
	_ = a
	cfg := twoSwitches + "resource \"tplinkeasysmart_led\" \"b\" {\n  switch  = \"sw02\"\n  enabled = false\n}\n" +
		"resource \"tplinkeasysmart_port\" \"p\" {\n  switch = \"sw01\"\n  port = 7\n  allow_disruptive = true\n}\n"
	b.ports = []escp.Port{{ID: 7, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"}}
	fakes["10.0.0.1"].(*fakeClient).ports = []escp.Port{{ID: 7, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, nil),
		Steps: []resource.TestStep{
			{Config: cfg},
			{ResourceName: "tplinkeasysmart_led.b", ImportState: true, ImportStateId: "sw02:led", ImportStateVerify: true},
			{ResourceName: "tplinkeasysmart_port.p", ImportState: true, ImportStateId: "sw01:7", ImportStateVerify: true,
				ImportStateVerifyIdentifierAttribute: "port", ImportStateVerifyIgnore: []string{"allow_disruptive"}},
		},
	})
}

// A switch name that only becomes known during apply cannot be checked while
// planning; it must not block the plan, and the apply must use it.
func TestSwitchNameKnownOnlyAtApplyIsAccepted(t *testing.T) {
	clearProviderEnv(t)
	a, b, fakes := twoFakes()
	cfg := twoSwitches + `
resource "terraform_data" "target" {
  input = "sw02"
}
resource "tplinkeasysmart_led" "x" {
  switch  = terraform_data.target.output
  enabled = true
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, nil),
		Steps:                    []resource.TestStep{{Config: cfg, Check: resource.TestCheckResourceAttr("tplinkeasysmart_led.x", "id", "sw02:led")}},
	})
	if b.sets != 1 || a.sets != 0 {
		t.Fatalf("the switch resolved at apply time must be the one written: sw01 sets=%d sw02 sets=%d", a.sets, b.sets)
	}
}
