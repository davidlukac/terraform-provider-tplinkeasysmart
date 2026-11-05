package regression

import (
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/escp-go"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// lender hands one switch back and forth between the provider and the suite's own clients. The
// library allows one client per switch in a process (escp.ErrSwitchInUse), and the suite checks the
// switch behind the provider's back with clients of its own: they step aside, which Close does by
// releasing the claim, while the provider has the switch, and take it back, by closing the provider's
// clients, before the suite reads or writes. A closed client opens again at its next call, so the
// handover needs nothing else. The provider is idle whenever the suite calls takeBack (between
// Terraform commands), and closing a client twice is harmless, so the provider's own Close of a
// previous configuration does no harm.
type lender struct {
	mu    sync.Mutex
	aside []*escp.Client // the suite's own clients, closed when the provider dials
	lent  []*escp.Client // the clients the provider was given, closed by takeBack
}

// stepAside registers a client of the suite that must not hold the switch while the provider does.
func (l *lender) stepAside(clients ...*escp.Client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.aside = append(l.aside, clients...)
}

// dial is the provider's dialer: the suite's clients step aside, open builds the provider's client,
// and the client is remembered to be taken back.
func (l *lender) dial(cfg escp.Config, open func(escp.Config) (*escp.Client, error)) (provider.SwitchClient, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.aside {
		_ = c.Close() // best effort: releases the claim; the client opens again at its next call
	}
	c, err := open(cfg)
	if err != nil {
		return nil, err
	}
	l.lent = append(l.lent, c)
	return c, nil
}

// takeBack closes the clients the provider was given, which releases their claim.
func (l *lender) takeBack() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.lent {
		_ = c.Close() // best effort: the provider is idle
	}
	l.lent = nil
}

// runTestCase is resource.Test; the tests of the handover replace it, because resource.Test needs a
// Terraform binary and TF_ACC.
var runTestCase = func(t *testing.T, tc resource.TestCase) { resource.Test(t, tc) }

// run is resource.Test with the switch taken back before every check and before every step's own
// preparation, and at the end, also when the test fails (resource.Test ends it with FailNow), so a
// read or a write of the suite's own never meets the provider's client.
func (l *lender) run(t *testing.T, tc resource.TestCase) {
	t.Helper()
	defer l.takeBack()
	steps := make([]resource.TestStep, len(tc.Steps))
	for i, st := range tc.Steps {
		if pre := st.PreConfig; pre != nil {
			st.PreConfig = func() { l.takeBack(); pre() }
		}
		if check := st.Check; check != nil {
			st.Check = func(state *terraform.State) error { l.takeBack(); return check(state) }
		}
		steps[i] = st
	}
	tc.Steps = steps
	runTestCase(t, tc)
}
