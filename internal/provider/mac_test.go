package provider

import (
	"context"
	"net"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// Switch 03's MAC, written the three ways a person may write it. The first is the canonical form.
const (
	macColon  = "c0:3a:55:6c:81:61"
	macHyphen = "C0-3A-55-6C-81-61"
)

var wantMAC = net.HardwareAddr{0xc0, 0x3a, 0x55, 0x6c, 0x81, 0x61}

// recordingFactories builds a provider whose client constructor records every configuration it is
// given, in order, and hands back an empty fake. It is what the MAC tests need: switches without a
// host, which the lookup-by-host fakes of the multi-switch tests cannot serve.
func recordingFactories(log *configLog) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{
			version: "test",
			newClient: func(c escp.Config) (SwitchClient, error) {
				log.mu.Lock()
				log.cfgs = append(log.cfgs, c)
				log.mu.Unlock()
				return &fakeClient{}, nil
			},
		}),
	}
}

// firstRound returns the configurations of the first Configure call, which builds n clients. Terraform
// configures the provider again for every plan, apply and refresh of a test step, so the log holds
// that round several times over; every round must be the same.
func (l *configLog) firstRound(t *testing.T, n int) []escp.Config {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.cfgs) == 0 || len(l.cfgs)%n != 0 {
		t.Fatalf("want a multiple of %d clients, got %d", n, len(l.cfgs))
	}
	for i := n; i < len(l.cfgs); i++ {
		a, b := l.cfgs[i%n], l.cfgs[i]
		a.WriteGate, b.WriteGate = nil, nil
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("client %d of round %d is %+v, the first round built %+v", i%n, i/n, b, a)
		}
	}
	return l.cfgs[:n]
}

func runMACConfig(t *testing.T, hcl string, expectError string) *configLog {
	t.Helper()
	log := &configLog{}
	step := resource.TestStep{Config: hcl}
	if expectError != "" {
		step.ExpectError = regexp.MustCompile(expectError)
	}
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: recordingFactories(log), Steps: []resource.TestStep{step}})
	return log
}

func TestTheMACAloneNamesTheProvidersOwnSwitch(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("TPLINK_EASYSMART_MAC", macHyphen)
	t.Setenv("TPLINK_EASYSMART_USERNAME", "env-admin")
	t.Setenv("TPLINK_EASYSMART_PASSWORD", "env-secret")
	log := runMACConfig(t, `provider "tplinkeasysmart" {}`+"\n"+infoDataSource, "")
	cfgs := log.firstRound(t, 1)
	checkClientConfig(t, cfgs[0], escp.Config{MAC: wantMAC, Username: "env-admin", Password: "env-secret", Timeout: 3 * time.Second}, true)
}

func TestTheMACInTheConfigurationOverridesTheEnvironmentAndIsKeptNextToTheHost(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("TPLINK_EASYSMART_MAC", "aa:aa:aa:aa:aa:aa")
	log := runMACConfig(t, `provider "tplinkeasysmart" {
  host     = "192.0.2.13"
  mac      = "`+macHyphen+`"
  username = "admin"
  password = "x"
}
`+infoDataSource, "")
	checkClientConfig(t, log.firstRound(t, 1)[0], escp.Config{Host: "192.0.2.13", MAC: wantMAC, Username: "admin", Password: "x", Timeout: 3 * time.Second}, true)
}

// The format is refused while planning, before any client exists, whatever the spelling mistake.
func TestAMalformedMACIsRejectedAtPlanTime(t *testing.T) {
	clearProviderEnv(t)
	for name, mac := range map[string]string{
		"not hex":         "zz:3a:55:6c:81:61",
		"too short":       "c0:3a:55:6c:81",
		"too long":        "c0:3a:55:6c:81:61:00:01",
		"mixed separator": "c0:3a-55:6c:81:61",
		"no separator":    "c03a556c8161",
		"empty":           "",
	} {
		t.Run(name, func(t *testing.T) {
			log := runMACConfig(t, `provider "tplinkeasysmart" {
  mac      = "`+mac+`"
  username = "admin"
  password = "x"
}
`+infoDataSource, `(?s)Invalid MAC\s+address.*aa:bb:cc:dd:ee:ff`)
			if len(log.cfgs) != 0 {
				t.Fatalf("no client may be built for an invalid MAC, got %+v", log.cfgs)
			}
		})
	}
}

func TestAMalformedMACInTheEnvironmentIsReportedBeforeAClientIsBuilt(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("TPLINK_EASYSMART_MAC", "not-a-mac")
	t.Setenv("TPLINK_EASYSMART_USERNAME", "u")
	t.Setenv("TPLINK_EASYSMART_PASSWORD", "p")
	log := runMACConfig(t, `provider "tplinkeasysmart" {}`+"\n"+infoDataSource, `(?s)Invalid MAC\s+address`)
	if len(log.cfgs) != 0 {
		t.Fatalf("no client may be built for an invalid MAC, got %+v", log.cfgs)
	}
}

func TestAMalformedMACInASwitchEntryIsRejectedAtPlanTime(t *testing.T) {
	clearProviderEnv(t)
	log := runMACConfig(t, `provider "tplinkeasysmart" {
  switches = { a = { mac = "c0:3a:55", username = "u", password = "p" } }
}
data "tplinkeasysmart_system_info" "this" {
  switch = "a"
}`, `(?s)Invalid MAC\s+address`)
	if len(log.cfgs) != 0 {
		t.Fatalf("no client may be built for an invalid MAC, got %+v", log.cfgs)
	}
}

func TestAnEntryOfTheSwitchesMapNeedsAHostOrAMAC(t *testing.T) {
	clearProviderEnv(t)
	log := runMACConfig(t, `provider "tplinkeasysmart" {
  switches = { a = { username = "u", password = "p" } }
}
data "tplinkeasysmart_system_info" "this" {
  switch = "a"
}`, `(?s)Missing switch host or MAC\s+address.*Switch a needs`)
	if len(log.cfgs) != 0 {
		t.Fatalf("no client may be built for an entry without an address, got %+v", log.cfgs)
	}
}

// Entries may name their switch by MAC, by host or by both; each client gets exactly what was written.
func TestEntriesOfTheSwitchesMapMayUseAMACAHostOrBoth(t *testing.T) {
	clearProviderEnv(t)
	log := runMACConfig(t, `provider "tplinkeasysmart" {
  switches = {
    by_mac  = { mac = "`+macHyphen+`", username = "u", password = "p" }
    by_host = { host = "192.0.2.12", username = "u", password = "p" }
    both    = { host = "192.0.2.11", mac = "a8:42:a1:2a:de:4c", username = "u", password = "p" }
  }
}
data "tplinkeasysmart_system_info" "this" {
  switch = "by_mac"
}`, "")
	// Entries are built in name order: both, by_host, by_mac.
	want := []escp.Config{
		{Host: "192.0.2.11", MAC: net.HardwareAddr{0xa8, 0x42, 0xa1, 0x2a, 0xde, 0x4c}},
		{Host: "192.0.2.12"},
		{MAC: wantMAC},
	}
	cfgs := log.firstRound(t, len(want))
	for i, w := range want {
		w.Username, w.Password, w.Timeout = "u", "p", 3*time.Second
		checkClientConfig(t, cfgs[i], w, true)
	}
}

// One switch must not get two clients, however its MAC is spelled and wherever it is named.
func TestTheSameMACTwiceIsRefused(t *testing.T) {
	clearProviderEnv(t)
	cases := map[string]string{
		"two entries, two spellings": `provider "tplinkeasysmart" {
  switches = {
    a = { mac = "` + macColon + `", username = "u", password = "p" }
    b = { mac = "` + macHyphen + `", host = "192.0.2.99", username = "u", password = "p" }
  }
}`,
		"the own switch and an entry": `provider "tplinkeasysmart" {
  mac      = "` + macColon + `"
  username = "u"
  password = "p"
  switches = { a = { mac = "` + macHyphen + `", username = "u", password = "p" } }
}`,
	}
	for name, hcl := range cases {
		t.Run(name, func(t *testing.T) {
			log := runMACConfig(t, hcl+"\n"+infoDataSource, `(?s)Same switch configured twice.*same mac\s+"c0:3a:55:6c:81:61"`)
			// The first one is built before the second is found to be a duplicate; the second never is.
			if len(log.cfgs) > 1 {
				t.Fatalf("the duplicate must not get a client, got %d clients", len(log.cfgs))
			}
		})
	}
}

// A value that is only known after apply cannot name a switch during planning; the message names the attribute.
func TestAMACKnownOnlyAfterApplyIsNamed(t *testing.T) {
	clearProviderEnv(t)
	log := runMACConfig(t, `resource "terraform_data" "m" {
  input = "`+macColon+`"
}
provider "tplinkeasysmart" {
  mac      = terraform_data.m.output
  username = "u"
  password = "p"
}
`+infoDataSource, `Unknown switch mac`)
	if len(log.cfgs) != 0 {
		t.Fatalf("no client may be built while the MAC is unknown, got %+v", log.cfgs)
	}
	log = runMACConfig(t, `resource "terraform_data" "m" {
  input = "`+macColon+`"
}
provider "tplinkeasysmart" {
  switches = { a = { mac = terraform_data.m.output, username = "u", password = "p" } }
}
data "tplinkeasysmart_system_info" "this" {
  switch = "a"
}`, `Unknown switch mac`)
	if len(log.cfgs) != 0 {
		t.Fatalf("no client may be built while the MAC is unknown, got %+v", log.cfgs)
	}
}

func TestParseMAC(t *testing.T) {
	got, err := parseMAC(macHyphen)
	if err != nil || !reflect.DeepEqual(got, wantMAC) {
		t.Fatalf("parseMAC(%q) = %v, %v; want %v", macHyphen, got, err, wantMAC)
	}
	if got, err := parseMAC(""); got != nil || err != nil {
		t.Fatalf("an empty string means no MAC, got %v, %v", got, err)
	}
	// net.ParseMAC accepts these, but they are not a switch's MAC address.
	for _, s := range []string{"0000.5e00.5301", "02:00:5e:10:00:00:00:01", "c0:3a:55:6c:81:61:00:00"} {
		if _, err := parseMAC(s); err == nil {
			t.Errorf("parseMAC(%q) accepted a MAC that is not six bytes", s)
		}
	}
}

// The schema validator refuses a malformed MAC while planning. Configure still checks what it is
// handed, so a value that got past the schema (it is the same function, but nothing else
// guarantees it) never reaches a client as "no MAC", which would silently drop the identity pin.
func TestConfigureRefusesAMalformedMACThatGotPastTheSchema(t *testing.T) {
	clearProviderEnv(t)
	p := &tplinkProvider{newClient: func(escp.Config) (SwitchClient, error) {
		t.Fatal("no client may be built")
		return nil, nil
	}}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	entryType := objType.AttributeTypes["switches"].(tftypes.Map).ElementType.(tftypes.Object)
	entry := tftypes.NewValue(entryType, map[string]tftypes.Value{
		"host":                      tftypes.NewValue(tftypes.String, nil),
		"mac":                       tftypes.NewValue(tftypes.String, "bogus"),
		"username":                  tftypes.NewValue(tftypes.String, "u"),
		"password":                  tftypes.NewValue(tftypes.String, "p"),
		"timeout_seconds":           tftypes.NewValue(tftypes.Number, nil),
		"allow_unverified_firmware": tftypes.NewValue(tftypes.Bool, nil),
	})
	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"host":                      tftypes.NewValue(tftypes.String, nil),
		"mac":                       tftypes.NewValue(tftypes.String, nil),
		"username":                  tftypes.NewValue(tftypes.String, nil),
		"password":                  tftypes.NewValue(tftypes.String, nil),
		"timeout_seconds":           tftypes.NewValue(tftypes.Number, nil),
		"allow_unverified_firmware": tftypes.NewValue(tftypes.Bool, nil),
		"switches":                  tftypes.NewValue(objType.AttributeTypes["switches"], map[string]tftypes.Value{"a": entry}),
	})
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw}}, resp)
	if len(resp.Diagnostics) != 1 || resp.Diagnostics[0].Summary() != "Invalid MAC address" || !strings.Contains(resp.Diagnostics[0].Detail(), `a: "bogus" is not a MAC address`) {
		t.Fatalf("want one Invalid MAC address diagnostic naming the switch, got %v", resp.Diagnostics)
	}
	if resp.ResourceData != nil {
		t.Fatal("no client data may be handed out")
	}
}

func TestTheMACValidatorDescribesItself(t *testing.T) {
	v := validMAC()
	ctx := context.Background()
	if d := v.Description(ctx); !strings.Contains(d, "aa:bb:cc:dd:ee:ff") || v.MarkdownDescription(ctx) != d {
		t.Fatalf("description %q / %q", d, v.MarkdownDescription(ctx))
	}
}
