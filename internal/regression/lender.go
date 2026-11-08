package regression

import (
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/davidlukac/escp-go"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

// suiteClient is a client of the suite's own that can step aside and come back. The library allows one
// client per switch in a process (escp.ErrSwitchInUse) and, since escp-go v0.9.0, a closed client is
// closed for good (escp.ErrClosed): it does not open again at its next call. So the suite never holds on
// to a *escp.Client: it holds this handle, which keeps the current client and the way to dial a new one.
// release closes the client (which gives its claim on the switch up) and forgets it, and get dials a new
// one when there is none. With a library whose closed client reopens on use (v0.8.0) the behavior is the
// same, because nothing is ever used after its Close.
type suiteClient struct {
	mu   sync.Mutex
	dial func() (*escp.Client, error)
	c    *escp.Client // nil while the client is stepped aside
}

// get is the current client, dialed when the client has stepped aside. It fails with the error of the
// dial, escp.ErrSwitchInUse among them while another client of the process has the switch (with the
// library of v0.8.0 that error comes from the first call of the client instead).
func (h *suiteClient) get() (*escp.Client, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.c == nil {
		c, err := h.dial()
		if err != nil {
			return nil, err
		}
		h.c = c
	}
	return h.c, nil
}

// release closes the client, if there is one, and forgets it, so the next get dials a new client.
func (h *suiteClient) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.c != nil {
		_ = h.c.Close() // best effort: it gives the claim up, and nothing uses the client afterwards
		h.c = nil
	}
}

// lender hands one switch back and forth between the provider and the suite's own clients. The
// library allows one client per switch in a process (escp.ErrSwitchInUse), and the suite checks the
// switch behind the provider's back with clients of its own (suiteClient): they step aside, which
// closes them and releases the claim, when the provider dials, and the switch is taken back by closing
// the provider's clients, before the suite reads or writes. A closed client stays closed, so the
// suite's client is not reopened: its handle dials a new one at the next use, and anything that must
// outlive a hand-over (the readers, the safety net) asks the handle each time and keeps no client.
// The provider is idle whenever the suite calls takeBack (between Terraform commands), and closing a
// client twice is harmless, so the provider's own Close of a previous configuration does no harm.
type lender struct {
	mu    sync.Mutex
	aside []*suiteClient // the suite's own clients, released when the provider dials
	lent  []*escp.Client // the clients the provider was given, closed by takeBack
}

// stepAside registers a client of the suite that must not hold the switch while the provider does.
func (l *lender) stepAside(clients ...*suiteClient) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.aside = append(l.aside, clients...)
}

// dial is the provider's dialer: the suite's clients step aside, open builds the provider's client,
// and the client is remembered to be taken back.
func (l *lender) dial(cfg escp.Config, open func(escp.Config) (*escp.Client, error)) (provider.SwitchClient, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, h := range l.aside {
		h.release()
	}
	c, err := open(cfg)
	if err != nil {
		return nil, err
	}
	l.lent = append(l.lent, c)
	return c, nil
}

// takeBack closes the clients the provider was given, which releases their claim; the suite's clients
// are dialed again at their next use.
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
