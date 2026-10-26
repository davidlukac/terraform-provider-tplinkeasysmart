package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
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

// dhcpConfig is the resource with `dhcp = true`: no address is written, the lease is reported.
func dhcpConfig(allow bool) string {
	return providerBlock + fmt.Sprintf("resource \"tplinkeasysmart_management_ip\" \"m\" {\n  dhcp = true\n  allow_disruptive = %t\n}\n", allow)
}

func TestAccManagementIPDHCPLifecycle(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			// Static to a lease: the leased values are what the resource reports.
			{Config: dhcpConfig(true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "dhcp", "true"),
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "ip_address", "192.0.2.85"),
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "netmask", "255.255.255.0"),
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "gateway", "192.0.2.1"),
			)},
			// Nothing to do when it already leases (and the plan after the apply is empty, which the harness requires).
			{Config: dhcpConfig(false)},
			{ResourceName: "tplinkeasysmart_management_ip.m", ImportState: true, ImportStateId: "management_ip", ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{"allow_disruptive"}},
			// A renewal that hands out another address is not drift: the configuration names no address.
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.ip = "192.0.2.86"; e.mu.Unlock() }, Config: dhcpConfig(false), PlanOnly: true},
			// And back to a static address.
			{Config: managementIPConfig("192.0.2.13", "255.255.255.0", "192.0.2.1", true), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "dhcp", "false"),
				resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "ip_address", "192.0.2.13"),
			)},
		},
	})
	e := f.extra()
	if e.dhcpCalls != 1 || e.ipSetCalls != 1 || !e.static || e.ip != "192.0.2.13" {
		t.Fatalf("dhcp writes=%d static writes=%d static=%t ip=%q, want one of each and the switch back at 192.0.2.13", e.dhcpCalls, e.ipSetCalls, e.static, e.ip)
	}
}

func TestAccManagementIPDHCPToStaticAtTheLeasedAddress(t *testing.T) {
	// The flag has to change even when the address is the one that was leased.
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	f.extra().static = false
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{Config: managementIPConfig("192.0.2.13", "255.255.255.0", "192.0.2.1", true),
			Check: resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "dhcp", "false")}},
	})
	if e := f.extra(); !e.static || e.ipSetCalls != 1 {
		t.Fatalf("static=%t writes=%d, want the flag turned off by one write", e.static, e.ipSetCalls)
	}
}

func TestAccManagementIPRefusesToChangeBetweenLeaseAndStaticWithoutAllowDisruptive(t *testing.T) {
	cases := []struct {
		name   string
		leases bool
		cfg    string
	}{
		{"static to lease", false, dhcpConfig(false)},
		{"lease to static", true, managementIPConfig("192.0.2.14", "255.255.255.0", "192.0.2.1", false)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
			f.extra().static = !c.leases
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: c.cfg, ExpectError: regexp.MustCompile(`Refusing to change the management address`)}},
			})
			if e := f.extra(); e.ipSetCalls != 0 || e.dhcpCalls != 0 {
				t.Fatalf("a refused change must not write, got %d static and %d DHCP writes", e.ipSetCalls, e.dhcpCalls)
			}
		})
	}
}

func TestAccManagementIPDHCPPlanValidation(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"an address next to dhcp", "dhcp = true\n  ip_address = \"192.0.2.14\"", `(?s)A static address next to\s+DHCP.*ip_address`},
		{"a mask next to dhcp", "dhcp = true\n  netmask = \"255.255.255.0\"", `(?s)A static address next to\s+DHCP.*netmask`},
		{"no address without dhcp", "dhcp = false\n  netmask = \"255.255.255.0\"\n  gateway = \"192.0.2.1\"", `(?s)Missing static address.*ip_address`},
		{"nothing at all", "allow_disruptive = true", `(?s)Missing static address`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps: []resource.TestStep{{Config: providerBlock + "resource \"tplinkeasysmart_management_ip\" \"m\" {\n  " + c.body + "\n}\n",
					PlanOnly: true, ExpectError: regexp.MustCompile(c.want)}},
			})
			if e := f.extra(); e.ipSetCalls != 0 || e.dhcpCalls != 0 {
				t.Fatalf("plan-time validation must stop before any write, got %d static and %d DHCP writes", e.ipSetCalls, e.dhcpCalls)
			}
		})
	}
}

// A flag that is only known after another resource is applied cannot be checked yet; it is checked
// once it is known.
func TestAccManagementIPDHCPKnownOnlyAfterApply(t *testing.T) {
	f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{{Config: providerBlock + `resource "terraform_data" "d" {
  input = true
}
resource "tplinkeasysmart_management_ip" "m" {
  dhcp             = terraform_data.d.output
  allow_disruptive = true
}
`, Check: resource.TestCheckResourceAttr("tplinkeasysmart_management_ip.m", "dhcp", "true")}},
	})
}

func TestAccManagementIPDHCPErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*extraState)
		want  string
	}{
		{"the client needs a MAC", func(e *extraState) { e.dhcpErr = fmt.Errorf("%w: EnableDHCP needs Config.MAC", escp.ErrInvalid) }, `(?s)Set\s+.mac.\s+in\s+the\s+provider\s+configuration`},
		{"the write fails", func(e *extraState) { e.dhcpErr = errBoom }, `(?s)look\s+for\s+it\s+by\s+its\s+MAC`},
		{"the switch stays static", func(e *extraState) { e.dhcpIgnore = true }, `did not change as requested`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newStaticFake("192.0.2.13", "255.255.255.0", "192.0.2.1")
			c.setup(f.extra())
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: dhcpConfig(true), ExpectError: regexp.MustCompile(c.want)}},
			})
		})
	}
}
