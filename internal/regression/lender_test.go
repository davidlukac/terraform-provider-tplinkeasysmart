package regression

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// The provider's client and the suite's own cannot both hold the switch: the library refuses the
// second (ErrSwitchInUse). The suite's client steps aside when the provider dials and takes the
// switch back, with a new client, when the provider's clients are closed. The test holds with a
// library whose closed client reopens on use (v0.8.0) and with one whose Close is final (v0.9.0):
// the refusal comes from the dial of the new client or from its first call, depending on the library.
func TestTheProviderAndTheSuiteTakeTurnsWithTheSwitch(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	own := s.Client()
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
	if _, err := ledOf(ctx, s.suite); !errors.Is(err, escp.ErrSwitchInUse) {
		t.Errorf("the suite read while the provider holds the switch: err = %v, want ErrSwitchInUse", err)
	}
	s.own()
	again := s.Client()
	if again == own {
		t.Error("the suite got its old client back after the hand-over, want a new one")
	}
	if _, err := again.LED(ctx); err != nil {
		t.Errorf("the suite's client cannot read after taking the switch back: %v", err)
	}
	if len(s.lent) != 0 {
		t.Errorf("%d clients still lent after taking the switch back", len(s.lent))
	}
	// What outlives the hand-over follows the suite's client: the readers and the safety net.
	if diff, err := s.Restored(ctx); err != nil || len(diff) != 0 {
		t.Errorf("the readers after the hand-over: diff %q, err %v", diff, err)
	}
	if done, err := s.Rescue(ctx); err != nil || len(done) != 0 {
		t.Errorf("the safety net after the hand-over: done %q, err %v", done, err)
	}
}

// dialFake is a client of a fake of its own, for the tests of the handle.
func dialFake(t *testing.T, f *escptest.Switch) (*escp.Client, error) {
	t.Helper()
	return escp.Dial(escp.Config{Host: loopback, SwitchPort: f.UDPPort(), HostPort: escptest.FreeUDPPort(t),
		BroadcastAddr: escptest.Loopback(), Timeout: fakeTimeout, Username: escptest.Username, Password: escptest.Password})
}

// A handle dials once, keeps the client until it steps aside and dials a new one after that; stepping
// aside gives the switch up, whatever the library does with a closed client.
func TestASuiteClientIsDialedAgainAfterItStepsAside(t *testing.T) {
	f := startFake(t)
	dials := 0
	h := &suiteClient{dial: func() (*escp.Client, error) { dials++; return dialFake(t, f) }}
	t.Cleanup(h.release)
	h.release() // nothing to release before the first use
	first, err := h.get()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := h.get(); err != nil || again != first || dials != 1 {
		t.Fatalf("a second get returned %p (first %p), err %v, %d dials, want the same client and one dial", again, first, err, dials)
	}
	h.release()
	// The claim is given up: another client of the same switch can be made and used.
	other, err := dialFake(t, f)
	if err != nil {
		t.Fatalf("the switch is still claimed after the client stepped aside: %v", err)
	}
	if _, err := other.LED(context.Background()); err != nil {
		t.Errorf("the other client cannot read: %v", err)
	}
	_ = other.Close() // makes room for the new client of the handle
	second, err := h.get()
	if err != nil {
		t.Fatal(err)
	}
	if second == first || dials != 2 {
		t.Errorf("after stepping aside get returned the old client (%t) or dialed %d times, want a new client and two dials", second == first, dials)
	}
	if _, err := second.LED(context.Background()); err != nil {
		t.Errorf("the new client cannot read: %v", err)
	}
}

func TestASuiteClientThatCannotBeDialedKeepsNothing(t *testing.T) {
	boom := errors.New("no such switch")
	fail := true
	f := startFake(t)
	h := &suiteClient{dial: func() (*escp.Client, error) {
		if fail {
			return nil, boom
		}
		return dialFake(t, f)
	}}
	t.Cleanup(h.release)
	if _, err := h.get(); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the error of the dial", err)
	}
	fail = false
	if c, err := h.get(); err != nil || c == nil {
		t.Errorf("the next get after a failed dial: %v, %v, want a new client", c, err)
	}
}

// The session fails the test, with the cause, when the suite's client cannot be dialed.
func TestSessionClientFailsTheTestWhenNoClientCanBeDialed(t *testing.T) {
	boom := errors.New("no such switch")
	ft := &fatalRecorder{TB: t}
	s := &Session{tb: ft, suite: &suiteClient{dial: func() (*escp.Client, error) { return nil, boom }}}
	func() {
		defer func() { _ = recover() }()
		s.Client()
	}()
	if !ft.failed || !strings.Contains(ft.msg, boom.Error()) {
		t.Errorf("failed=%t msg=%q, want the test failed naming the cause", ft.failed, ft.msg)
	}
}

// What takes the client from the handle reports a client that cannot be dialed, and does nothing to a switch.
func TestWhatNeedsTheSuitesClientReportsOneThatCannotBeDialed(t *testing.T) {
	boom := errors.New("no such switch")
	s := &Session{suite: &suiteClient{dial: func() (*escp.Client, error) { return nil, boom }}}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"the safety net", func() error { _, err := s.Rescue(ctx); return err }},
		{"the capture", func() error { return s.capture(ctx) }},
		{"the readers", func() error { _, err := Take(ctx, CoreReaders(s.suite.get)); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, boom) {
				t.Errorf("err = %v, want the error of the dial", err)
			}
		})
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
			if _, err := s.Client().LED(ctx); err != nil {
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
	if _, err := s.Client().LED(ctx); err != nil {
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
