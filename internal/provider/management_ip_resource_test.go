package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func managementIPConfig(ip, mask, gw string, allow bool) string {
	return providerBlock + fmt.Sprintf("resource \"tplinkeasysmart_management_ip\" \"m\" {\n  ip_address = %q\n  netmask = %q\n  gateway = %q\n  allow_disruptive = %t\n}\n", ip, mask, gw, allow)
}

func TestAccManagementIPLifecycle(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			// Adopting the current address writes nothing.
			{Config: managementIPConfig("192.0.2.13", "255.255.255.0", "192.0.2.1", false), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "id", "management_ip"),
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "ip_address", "192.0.2.13"),
			)},
			{Config: managementIPConfig("192.0.2.14", "255.255.255.0", "192.0.2.2", true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "ip_address", "192.0.2.14"),
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "gateway", "192.0.2.2"),
			)},
			{ResourceName: "tplinkeasysmart_management_ip.m", ImportState: true, ImportStateId: "management_ip", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"allow_disruptive"}},
		},
	})
	e := f.extra()
	if e.ip != "192.0.2.14" || e.gateway != "192.0.2.2" || e.ipSetCalls != 1 {
		t.Fatalf("ip=%q gateway=%q writes=%d, want 192.0.2.14 / 192.0.2.2 after exactly one write", e.ip, e.gateway, e.ipSetCalls)
	}
}

func TestAccManagementIPMaskChange(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: managementIPConfig("192.0.2.13", "255.255.0.0", "192.0.2.1", true),
				Check: resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "netmask", "255.255.0.0")},
		},
	})
	if got := f.extra().netmask; got != "255.255.0.0" {
		t.Fatalf("netmask %q, want 255.255.0.0", got)
	}
}

func TestAccManagementIPRefusesWithoutAllowDisruptive(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: managementIPConfig("192.0.2.14", "255.255.255.0", "192.0.2.1", false), ExpectError: regexp.MustCompile(`Refusing to change the management address`)},
		},
	})
	if n := f.extra().ipSetCalls; n != 0 {
		t.Fatalf("a refused change must not write, got %d writes", n)
	}
}

func TestAccManagementIPRefusesDHCP(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	f.extra().static = false
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: managementIPConfig("192.0.2.14", "255.255.255.0", "192.0.2.1", true), ExpectError: regexp.MustCompile(`The switch uses DHCP`)},
		},
	})
	if n := f.extra().ipSetCalls; n != 0 {
		t.Fatalf("a DHCP switch must not be written, got %d writes", n)
	}
}

func TestAccManagementIPDriftIsDetected(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	cfg := managementIPConfig("192.0.2.13", "255.255.255.0", "192.0.2.1", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.gateway = "192.0.2.9"; e.mu.Unlock() }, Config: cfg, PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

func TestAccManagementIPPlanValidation(t *testing.T) {
	cases := []struct {
		name, ip, mask, gw, want string
	}{
		{"not an address", "not-an-ip", "255.255.255.0", "192.0.2.1", `not a dotted IPv4`},
		{"ipv6", "2001:db8::1", "255.255.255.0", "192.0.2.1", `not a dotted IPv4`},
		{"gap in mask", "192.0.2.13", "255.0.255.0", "192.0.2.1", `not a contiguous prefix`},
		{"mask too wide", "192.0.2.13", "128.0.0.0", "192.0.2.1", `not a contiguous prefix`},
		{"network address", "192.0.2.0", "255.255.255.0", "192.0.2.1", `not a usable host address`},
		{"broadcast address", "192.0.2.255", "255.255.255.0", "192.0.2.1", `not a usable host address`},
		{"gateway outside the subnet", "192.0.2.13", "255.255.255.0", "198.51.100.1", `must be another host address`},
		{"gateway is the switch", "192.0.2.13", "255.255.255.0", "192.0.2.13", `must be another host address`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: managementIPConfig(c.ip, c.mask, c.gw, true), PlanOnly: true, ExpectError: regexp.MustCompile(c.want)}},
			})
			if n := f.extra().ipSetCalls; n != 0 {
				t.Fatalf("plan-time validation must stop before any write, got %d", n)
			}
		})
	}
}

func TestAccManagementIPNoGatewayAllowed(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{Config: managementIPConfig("192.0.2.13", "255.255.255.0", "0.0.0.0", true),
			Check: resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "gateway", "0.0.0.0")}},
	})
}

func TestAccManagementIPErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*extraState)
		want  string
	}{
		{"info fails", func(e *extraState) { e.infoErr = errBoom }, `Reading the switch failed`},
		{"write fails", func(e *extraState) { e.ipSetErr = errBoom }, `look at both`},
		{"write ignored", func(e *extraState) { e.ipIgnoreSet = true }, `did not change as requested`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
			c.setup(f.extra())
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: managementIPConfig("192.0.2.14", "255.255.255.0", "192.0.2.1", true), ExpectError: regexp.MustCompile(c.want)}},
			})
		})
	}
}

func TestAccManagementIPReadFails(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	cfg := managementIPConfig("192.0.2.13", "255.255.255.0", "192.0.2.1", false)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: cfg},
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.infoErr = errBoom; e.mu.Unlock() }, Config: cfg, PlanOnly: true, ExpectError: regexp.MustCompile(`Reading the switch failed`)},
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.infoErr = nil; e.mu.Unlock() }, Config: cfg, PlanOnly: true},
		},
	})
}

// An address that is only known at apply time cannot be validated at plan time; apply validates it.
func TestAccManagementIPAddressKnownAfterApply(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	cfg := providerBlock + `
resource "terraform_data" "ip" { input = "192.0.2.14" }
resource "tplinkeasysmart_management_ip" "m" {
  ip_address       = terraform_data.ip.output
  netmask          = "255.255.255.0"
  gateway          = "192.0.2.1"
  allow_disruptive = true
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps:                    []resource.TestStep{{Config: cfg, Check: resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "ip_address", "192.0.2.14")}},
	})
}
