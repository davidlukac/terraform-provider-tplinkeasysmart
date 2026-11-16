package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// boolSettingCase describes one of the two data sources that read a single on/off setting of the switch
// and the resource that writes it.
type boolSettingCase struct {
	name     string // the type name without the provider prefix
	id       string // what `id` is
	method   string // the client call the data source reads with
	readFail string // the summary of the diagnostic when that call fails
	set      func(f *fakeClient, on bool)
	get      func(f *fakeClient) bool
	resource string // a resource block that sets the setting to "true", named "this"
}

func boolSettingCases() []boolSettingCase {
	return []boolSettingCase{
		{
			name: "led", id: "led", method: "LED", readFail: "Reading LED failed",
			set: func(f *fakeClient, on bool) { f.led = on }, get: func(f *fakeClient) bool { return f.led },
			resource: `resource "tplinkeasysmart_led" "this" {
  enabled = true
}
`,
		},
		{
			name: "loop_prevention", id: "loop_prevention", method: "LoopPrevention", readFail: "Reading loop prevention failed",
			set: func(f *fakeClient, on bool) { f.loop = on }, get: func(f *fakeClient) bool { return f.loop },
			resource: `resource "tplinkeasysmart_loop_prevention" "this" {
  enabled          = true
  allow_disruptive = true
}
`,
		},
	}
}

func dataSourceAddr(tc boolSettingCase) string { return "data.tplinkeasysmart_" + tc.name + ".this" }

func dataSourceBlock(tc boolSettingCase) string {
	return fmt.Sprintf("data \"tplinkeasysmart_%s\" \"this\" {}\n", tc.name)
}

// The data source reports what the switch has, in the same shape as the resource, and never writes.
func TestLEDAndLoopPreventionDataSourcesReportWhatTheSwitchHas(t *testing.T) {
	for _, tc := range boolSettingCases() {
		for _, on := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s enabled=%t", tc.name, on), func(t *testing.T) {
				f := &fakeClient{}
				tc.set(f, on)
				resource.UnitTest(t, resource.TestCase{
					ProtoV6ProviderFactories: factories(f),
					Steps: []resource.TestStep{{
						Config: providerBlock + dataSourceBlock(tc),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(dataSourceAddr(tc), "id", tc.id),
							resource.TestCheckResourceAttr(dataSourceAddr(tc), "enabled", fmt.Sprint(on)),
							resource.TestCheckNoResourceAttr(dataSourceAddr(tc), "switch"),
						),
					}},
				})
				if f.sets != 0 {
					t.Errorf("a data source must not write: the switch saw %d writes", f.sets)
				}
			})
		}
	}
}

// The data source and the resource of one setting report the same value and the same id.
func TestLEDAndLoopPreventionDataSourcesAgreeWithTheirResource(t *testing.T) {
	for _, tc := range boolSettingCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeClient{}
			tc.set(f, false)
			cfg := providerBlock + tc.resource + fmt.Sprintf(`data "tplinkeasysmart_%[1]s" "this" {
  depends_on = [tplinkeasysmart_%[1]s.this]
}
`, tc.name)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps: []resource.TestStep{{
					Config: cfg,
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(dataSourceAddr(tc), "enabled", "true"),
						resource.TestCheckResourceAttrPair(dataSourceAddr(tc), "enabled", "tplinkeasysmart_"+tc.name+".this", "enabled"),
						resource.TestCheckResourceAttrPair(dataSourceAddr(tc), "id", "tplinkeasysmart_"+tc.name+".this", "id"),
					),
				}},
			})
			if !tc.get(f) {
				t.Error("the resource did not write the setting the data source then read")
			}
		})
	}
}

// A change made on the switch behind Terraform's back shows in the next read.
func TestLEDAndLoopPreventionDataSourcesReadTheSwitchAgainOnEveryRun(t *testing.T) {
	for _, tc := range boolSettingCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeClient{}
			tc.set(f, true)
			cfg := providerBlock + dataSourceBlock(tc)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps: []resource.TestStep{
					{Config: cfg, Check: resource.TestCheckResourceAttr(dataSourceAddr(tc), "enabled", "true")},
					{
						PreConfig: func() { f.mu.Lock(); tc.set(f, false); f.mu.Unlock() },
						Config:    cfg,
						Check:     resource.TestCheckResourceAttr(dataSourceAddr(tc), "enabled", "false"),
					},
				},
			})
		})
	}
}

// A switch that cannot be read is reported by the data source, with the cause, and nothing is written.
func TestLEDAndLoopPreventionDataSourcesReportASwitchThatCannotBeRead(t *testing.T) {
	for _, tc := range boolSettingCases() {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeClient{}
			fc := newFaulty(inner)
			fc.failAlways(tc.method)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps: []resource.TestStep{{
					Config:      providerBlock + dataSourceBlock(tc),
					ExpectError: regexp.MustCompile(tc.readFail + `(?s:.*)` + regexp.QuoteMeta(errInjected.Error())),
				}},
			})
			if inner.sets != 0 {
				t.Errorf("a data source must not write: the switch saw %d writes", inner.sets)
			}
		})
	}
}

// With a `switches` map the data source reads the switch named by `switch`, and its id carries the name.
func TestLEDAndLoopPreventionDataSourcesReadTheSwitchTheyAreToldTo(t *testing.T) {
	clearProviderEnv(t)
	for _, tc := range boolSettingCases() {
		t.Run(tc.name, func(t *testing.T) {
			a, b, fakes := twoFakes()
			tc.set(a, true)
			tc.set(b, false)
			cfg := twoSwitches + fmt.Sprintf(`
locals {
  names = ["sw01", "sw02"]
}
data "tplinkeasysmart_%s" "x" {
  count  = 2
  switch = local.names[count.index]
}
`, tc.name)
			addr := "data.tplinkeasysmart_" + tc.name + ".x"
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: multiFactories(fakes, nil),
				Steps: []resource.TestStep{{Config: cfg, Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr+".0", "enabled", "true"),
					resource.TestCheckResourceAttr(addr+".0", "switch", "sw01"),
					resource.TestCheckResourceAttr(addr+".0", "id", "sw01:"+tc.id),
					resource.TestCheckResourceAttr(addr+".1", "enabled", "false"),
					resource.TestCheckResourceAttr(addr+".1", "switch", "sw02"),
					resource.TestCheckResourceAttr(addr+".1", "id", "sw02:"+tc.id),
				)}},
			})
			if a.sets != 0 || b.sets != 0 {
				t.Errorf("a data source must not write: sw01 saw %d writes, sw02 saw %d", a.sets, b.sets)
			}
		})
	}
}

// With a `switches` map an omitted or unknown `switch` is refused, and no switch is read.
func TestLEDAndLoopPreventionDataSourcesRefuseAnUnresolvableSwitch(t *testing.T) {
	clearProviderEnv(t)
	cases := []struct {
		name string
		arg  string
		want string
	}{
		{"omitted", "", `(?s)set\s+.switch.\s+to\s+one\s+of`},
		{"unknown", "  switch = \"nope\"\n", `unknown switch "nope"`},
	}
	for _, bc := range boolSettingCases() {
		for _, tc := range cases {
			t.Run(bc.name+" "+tc.name, func(t *testing.T) {
				_, _, fakes := twoFakes()
				cfg := twoSwitches + fmt.Sprintf("data \"tplinkeasysmart_%s\" \"x\" {\n%s}\n", bc.name, tc.arg)
				resource.UnitTest(t, resource.TestCase{
					ProtoV6ProviderFactories: multiFactories(fakes, nil),
					Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(tc.want)}},
				})
			})
		}
	}
}
