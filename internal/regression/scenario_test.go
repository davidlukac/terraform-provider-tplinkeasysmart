package regression

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// The scenarios run through a real Terraform binary against the target chosen by the environment
// (the fake by default). Run them with `make regression` (TF_ACC=1); `make regression-live`
// aims them at switch 03. Each scenario changes a setting, checks it by reading the switch
// directly (not through the provider), and puts the original value back with an explicit
// configuration, because destroying a resource only forgets it. The last check of every
// scenario is the readout of the whole switch against the one taken before the first scenario.

// scenario runs the two steps every scenario has: change and verify through the library, then
// restore the original values by configuration and prove the readout is identical.
func (s *Session) scenario(t *testing.T, change, restore string, verifyChange resource.TestCheckFunc) {
	t.Helper()
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: s.Factories(),
		Steps: []resource.TestStep{
			{Config: s.ProviderHCL() + change, Check: verifyChange},
			{Config: s.ProviderHCL() + restore, Check: s.restoredCheck(t)},
		},
	})
}

// restoredCheck fails with the differing settings named when the switch is not as it was.
func (s *Session) restoredCheck(t *testing.T) resource.TestCheckFunc {
	return func(*terraform.State) error {
		diff, err := s.Restored(context.Background())
		if err != nil {
			return err
		}
		if len(diff) > 0 {
			return fmt.Errorf("the switch is not as it was before the run: %q", diff)
		}
		return nil
	}
}

func openForScenario(t *testing.T) *Session {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("scenarios need TF_ACC=1 (make regression)")
	}
	s := Open(t, os.Getenv)
	t.Log(s.Describe())
	return s
}

func TestScenarioLED(t *testing.T) {
	s := openForScenario(t)
	orig, err := s.Client.LED(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flipped := !orig
	cfg := func(on bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_led\" \"this\" {\n  enabled = %t\n}\n", on)
	}
	s.scenario(t, cfg(flipped), cfg(orig), func(*terraform.State) error {
		got, err := s.Client.LED(context.Background())
		if err != nil || got != flipped {
			return fmt.Errorf("LED on the switch = %t, err %w, want %t", got, err, flipped)
		}
		return nil
	})
}

func TestScenarioIGMPSnooping(t *testing.T) {
	s := openForScenario(t)
	orig, err := s.Client.IGMP(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	enabled, suppression := orig.Enabled, orig.ReportSuppression
	cfg := func(e, r bool) string {
		return fmt.Sprintf("resource \"tplinkeasysmart_igmp_snooping\" \"this\" {\n  enabled                    = %t\n  report_message_suppression = %t\n}\n", e, r)
	}
	s.scenario(t, cfg(!enabled, !suppression), cfg(enabled, suppression), func(*terraform.State) error {
		got, err := s.Client.IGMP(context.Background())
		if err != nil || got.Enabled != !enabled || got.ReportSuppression != !suppression {
			return fmt.Errorf("IGMP on the switch = %+v, err %w, want enabled=%t suppression=%t", got, err, !enabled, !suppression)
		}
		return nil
	})
}
