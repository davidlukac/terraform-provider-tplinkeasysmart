package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// relocate_by_mac reaches the library's Config.RelocateByMAC: off by default, set at the provider,
// overridden per entry of `switches`.
func TestRelocateByMACReachesTheClientConfig(t *testing.T) {
	clearProviderEnv(t)
	a, b, fakes := twoFakes()
	_, _ = a, b
	log := &configLog{}
	cfg := `
provider "tplinkeasysmart" {
  username        = "admin"
  password        = "pw"
  relocate_by_mac = true
  switches = {
    sw01 = { host = "10.0.0.1", mac = "` + macHyphen + `", username = "admin", password = "one" }
    sw02 = { host = "10.0.0.2", mac = "00:00:5e:00:53:02", username = "admin", password = "two", relocate_by_mac = false }
  }
}
data "tplinkeasysmart_ports" "x" {
  switch = "sw01"
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, log),
		Steps:                    []resource.TestStep{{Config: cfg, PlanOnly: true}},
	})
	got := log.byHost()
	if !got["10.0.0.1"].RelocateByMAC || got["10.0.0.2"].RelocateByMAC {
		t.Fatalf("the provider default must apply to sw01 and the entry must override it for sw02: %+v", got)
	}
}

func TestRelocateByMACIsOffByDefault(t *testing.T) {
	clearProviderEnv(t)
	_, _, fakes := twoFakes()
	log := &configLog{}
	cfg := `
provider "tplinkeasysmart" {
  host     = "10.0.0.1"
  mac      = "` + macHyphen + `"
  username = "admin"
  password = "pw"
}
data "tplinkeasysmart_ports" "x" {}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: multiFactories(fakes, log),
		Steps:                    []resource.TestStep{{Config: cfg, PlanOnly: true}},
	})
	if log.byHost()["10.0.0.1"].RelocateByMAC {
		t.Fatal("the client must keep to the address unless relocate_by_mac is set")
	}
}

func TestRelocateByMACNeedsTheMAC(t *testing.T) {
	clearProviderEnv(t)
	_, _, fakes := twoFakes()
	for name, cfg := range map[string]string{
		"the own switch": `
provider "tplinkeasysmart" {
  host            = "10.0.0.1"
  username        = "admin"
  password        = "pw"
  relocate_by_mac = true
}
data "tplinkeasysmart_ports" "x" {}
`,
		"a switch entry": `
provider "tplinkeasysmart" {
  switches = { a = { host = "10.0.0.1", username = "u", password = "p", relocate_by_mac = true } }
}
data "tplinkeasysmart_ports" "x" {
  switch = "a"
}
`,
	} {
		t.Run(name, func(t *testing.T) { // subtests: the map names the cases
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: multiFactories(fakes, nil),
				Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`(?s)relocate_by_mac\s+needs\s+mac`)}},
			})
		})
	}
}

// The environment variable is the default of the provider's own switch and of the entries; the
// argument wins, and a value that is not a boolean stops the run.
func TestRelocateByMACFromTheEnvironment(t *testing.T) {
	cfg := `
provider "tplinkeasysmart" {
  host     = "10.0.0.1"
  mac      = "` + macHyphen + `"
  username = "admin"
  password = "pw"
%s}
data "tplinkeasysmart_ports" "x" {}
`
	for _, tt := range []struct {
		name, env, arg, wantErr string
		want                    bool
	}{
		{name: "true turns it on", env: "true", want: true},
		{name: "false, like nothing, keeps it off", env: "false"},
		{name: "the argument wins", env: "true", arg: "  relocate_by_mac = false\n"},
		{name: "a value that is not a boolean", env: "yes", wantErr: `(?s)Invalid relocate_by_mac.*TPLINK_EASYSMART_RELOCATE_BY_MAC\s+is\s+"yes"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			clearProviderEnv(t)
			t.Setenv(envRelocateByMAC, tt.env)
			_, _, fakes := twoFakes()
			log := &configLog{}
			step := resource.TestStep{Config: fmt.Sprintf(cfg, tt.arg), PlanOnly: true}
			if tt.wantErr != "" {
				step.ExpectError = regexp.MustCompile(tt.wantErr)
			}
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: multiFactories(fakes, log), Steps: []resource.TestStep{step}})
			if tt.wantErr == "" && log.byHost()["10.0.0.1"].RelocateByMAC != tt.want {
				t.Fatalf("RelocateByMAC = %v, want %v", !tt.want, tt.want)
			}
		})
	}
}
