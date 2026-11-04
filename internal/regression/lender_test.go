package regression

import (
	"context"
	"errors"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/escp-go"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// The provider's client and the suite's own cannot both hold the switch: the library refuses the
// second (ErrSwitchInUse). The suite's client steps aside when the provider dials and takes the
// switch back when the provider's clients are closed.
func TestTheProviderAndTheSuiteTakeTurnsWithTheSwitch(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	own := s.Client
	if _, err := own.LED(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := escp.Config{Host: loopback, Username: s.Target.Username, Password: s.Target.Password}
	lent, err := s.dial(cfg)
	if err != nil {
		t.Fatalf("the provider could not dial while the suite's client was open: %v", err)
	}
	if _, err := lent.LED(ctx); err != nil {
		t.Fatalf("the provider's client cannot read: %v", err)
	}
	if _, err := own.LED(ctx); !errors.Is(err, escp.ErrSwitchInUse) {
		t.Errorf("the suite's client read while the provider holds the switch: err = %v, want ErrSwitchInUse", err)
	}
	s.own()
	if _, err := own.LED(ctx); err != nil {
		t.Errorf("the suite's client cannot read after taking the switch back: %v", err)
	}
	if len(s.lent) != 0 {
		t.Errorf("%d clients still lent after taking the switch back", len(s.lent))
	}
}

func TestADialThatFailsLendsNothing(t *testing.T) {
	var l lender
	boom := errors.New("no such switch")
	if _, err := l.dial(escp.Config{}, func(escp.Config) (*escp.Client, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the error of the dialer", err)
	}
	if len(l.lent) != 0 {
		t.Errorf("a failed dial lent %d clients", len(l.lent))
	}
}

// run wraps the preparation and the check of every step, so the suite's own reads and writes never meet the
// provider's client, and takes the switch back at the end, whatever the case did.
func TestRunTakesTheSwitchBackAroundEveryStep(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	cfg := escp.Config{Host: loopback, Username: s.Target.Username, Password: s.Target.Password}
	var ran []string
	read := func(step string) func() {
		return func() {
			if _, err := s.Client.LED(ctx); err != nil {
				t.Errorf("%s: the suite's client cannot read: %v", step, err)
			}
			ran = append(ran, step)
		}
	}
	old := runTestCase
	t.Cleanup(func() { runTestCase = old })
	// What Terraform and the provider do around a step: the preparation, then a command in which the provider
	// closes the clients of its previous configuration and dials, then the check.
	runTestCase = func(_ *testing.T, tc resource.TestCase) {
		var previous provider.SwitchClient
		command := func(step int) {
			if previous != nil {
				_ = previous.Close() // best effort: the provider's own Close of a previous configuration
			}
			c, err := s.dial(cfg)
			if err != nil {
				t.Fatalf("step %d: the provider could not dial: %v", step, err)
			}
			previous = c
		}
		for i, st := range tc.Steps {
			if st.PreConfig != nil {
				st.PreConfig()
			}
			command(i)
			if st.Check != nil {
				if err := st.Check(&terraform.State{}); err != nil {
					t.Errorf("step %d check: %v", i, err)
				}
			}
		}
		command(len(tc.Steps)) // the last provider client is still open when the case ends
	}
	s.test(t, resource.TestCase{Steps: []resource.TestStep{
		{PreConfig: read("pre"), Check: func(*terraform.State) error { read("check")(); return nil }},
		{}, // no preparation and no check: nothing to wrap
	}})
	if len(ran) != 2 {
		t.Errorf("ran %v, want the preparation and the check of the first step", ran)
	}
	if _, err := s.Client.LED(ctx); err != nil {
		t.Errorf("the suite's client cannot read after the case: %v", err)
	}
}

func TestTestUsesTheSessionsProviderUnlessTheCaseNamesOne(t *testing.T) {
	s := Open(t, noEnv)
	old := runTestCase
	t.Cleanup(func() { runTestCase = old })
	var got resource.TestCase
	runTestCase = func(_ *testing.T, tc resource.TestCase) { got = tc }
	s.test(t, resource.TestCase{})
	if _, ok := got.ProtoV6ProviderFactories["tplinkeasysmart"]; !ok {
		t.Errorf("factories = %v, want the session's provider", got.ProtoV6ProviderFactories)
	}
}

// The runner the suite uses is resource.Test: without TF_ACC it skips the test, which is all this
// can prove without a Terraform binary.
func TestTheDefaultRunnerIsResourceTest(t *testing.T) {
	t.Setenv("TF_ACC", "")
	var skipped bool
	t.Run("without TF_ACC", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }() // runs when the skip ends the subtest
		runTestCase(t, resource.TestCase{
			ProtoV6ProviderFactories: Open(t, noEnv).Factories(),
			Steps:                    []resource.TestStep{{Config: "# nothing"}},
		})
	})
	if !skipped {
		t.Error("the default runner did not skip without TF_ACC, so it is not resource.Test")
	}
}
