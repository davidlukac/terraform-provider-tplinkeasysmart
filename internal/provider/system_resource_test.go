package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func systemConfig(name string) string {
	return providerBlock + fmt.Sprintf("resource \"tplinkeasysmart_system\" \"s\" {\n  hostname = %q\n}\n", name)
}

func TestAccSystemLifecycle(t *testing.T) {
	f := newHostnameFake("TL-SG108E-01")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: systemConfig("sw01"), Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("tplinkeasysmart_system.s", "id", "system"),
				resource.TestCheckResourceAttr("tplinkeasysmart_system.s", "hostname", "sw01"),
			)},
			{Config: systemConfig("sw01-b"), Check: resource.TestCheckResourceAttr("tplinkeasysmart_system.s", "hostname", "sw01-b")},
			{ResourceName: "tplinkeasysmart_system.s", ImportState: true, ImportStateId: "system", ImportStateVerify: true},
		},
	})
	e := f.extra()
	if e.hostname != "sw01-b" || e.setCalls != 2 {
		t.Fatalf("hostname=%q writes=%d, want sw01-b after exactly two writes", e.hostname, e.setCalls)
	}
}

func TestAccSystemSkipsWriteWhenAlreadyCorrect(t *testing.T) {
	f := newHostnameFake("TL-SG108E-01")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps:                    []resource.TestStep{{Config: systemConfig("TL-SG108E-01")}},
	})
	if n := f.extra().setCalls; n != 0 {
		t.Fatalf("adopting the current name must not write to the switch, got %d writes", n)
	}
}

func TestAccSystemDriftIsDetected(t *testing.T) {
	f := newHostnameFake("sw01")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: systemConfig("sw01")},
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.hostname = "renamed-in-web-ui"; e.mu.Unlock() },
				Config: systemConfig("sw01"), PlanOnly: true, ExpectNonEmptyPlan: true},
		},
	})
}

func TestAccSystemErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*extraState)
		cfg   string
		want  string
	}{
		{"invalid name never reaches the switch", func(*extraState) {}, "bad.name", `invalid device description`},
		{"read fails before writing", func(e *extraState) { e.infoErr = errBoom }, "sw01", `Reading the switch failed`},
		{"write fails", func(e *extraState) { e.setErr = errBoom }, "sw01", `Setting the device description failed`},
		{"device ignores the write", func(e *extraState) { e.ignoreSet = true }, "sw01", `did not change`},
		{"read-back fails after a successful write", func(e *extraState) { e.infoErrAfterSet = true }, "sw01", `Reading the device description back failed`},
		{"IP changes unexpectedly", func(e *extraState) { e.mutateIP = true }, "sw01", `changed unexpectedly`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newHostnameFake("TL-SG108E-01")
			tc.setup(f.extra())
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(f),
				Steps:                    []resource.TestStep{{Config: systemConfig(tc.cfg), ExpectError: regexp.MustCompile(tc.want)}},
			})
		})
	}
}

func TestAccSystemImportRejectsOtherIDs(t *testing.T) {
	f := newHostnameFake("sw01")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: systemConfig("sw01")},
			{ResourceName: "tplinkeasysmart_system.s", ImportState: true, ImportStateId: "nope",
				ExpectError: regexp.MustCompile(`only valid id is "system"`)},
		},
	})
}

func TestAccSystemRefreshError(t *testing.T) {
	f := newHostnameFake("sw01")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(f),
		Steps: []resource.TestStep{
			{Config: systemConfig("sw01")},
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.infoErr = errBoom; e.mu.Unlock() },
				Config: systemConfig("sw01"), ExpectError: regexp.MustCompile(`Reading the switch failed`)},
			{PreConfig: func() { e := f.extra(); e.mu.Lock(); e.infoErr = nil; e.mu.Unlock() }, Config: systemConfig("sw01")},
		},
	})
}
