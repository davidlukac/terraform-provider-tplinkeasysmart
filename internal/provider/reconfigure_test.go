package provider

import (
	"errors"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/escp-go"
)

// liveClients is a dialer that behaves like the library of escp-go 0.8.0: a second client of a
// switch that the first one has not closed is refused.
type liveClients struct {
	mu    sync.Mutex
	open  map[string]*trackedClient
	dials int
}

type trackedClient struct {
	*fakeClient
	host string
	set  *liveClients
}

func (t *trackedClient) Close() error {
	t.set.mu.Lock()
	defer t.set.mu.Unlock()
	if t.set.open[t.host] == t {
		delete(t.set.open, t.host)
	}
	return t.fakeClient.Close()
}

func (l *liveClients) dial(c escp.Config) (SwitchClient, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.dials++
	if l.open[c.Host] != nil {
		return nil, errors.New("escp: another client of this process already talks to that switch")
	}
	t := &trackedClient{fakeClient: &fakeClient{}, host: c.Host, set: l}
	l.open[c.Host] = t
	return t, nil
}

// Terraform configures the provider again for every step of a test and every walk of a run, in
// one process. The clients of the previous configuration are closed first, so the library's
// refusal of a second client of a switch does not turn the second Configure into an error.
func TestReconfiguringTheProviderClosesThePreviousClients(t *testing.T) {
	live := &liveClients{open: map[string]*trackedClient{}}
	factories := map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{version: "test", newClient: live.dial}),
	}
	cfg := providerBlock + infoDataSource
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{Config: cfg},
			{Config: cfg},
		},
	})
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.dials < 3 {
		t.Fatalf("the provider was configured %d times, want at least once per step", live.dials)
	}
	if len(live.open) > 1 {
		t.Fatalf("%d clients of the same switch are open, want at most the last one", len(live.open))
	}
}

func TestACloseThatFailsDoesNotStopTheNextConfiguration(t *testing.T) {
	healthy := &fakeClient{}
	reg := &switchRegistry{def: &failingClose{}, named: map[string]SwitchClient{"a": healthy, "b": &failingClose{}}}
	if err := reg.closeAll(); err == nil || err.Error() != "close failed" {
		t.Fatalf("got %v, want the first failure", err)
	}
	if healthy.closed != 1 {
		t.Fatal("every client is closed even when another one fails")
	}
}

type failingClose struct{ fakeClient }

func (*failingClose) Close() error { return errors.New("close failed") }

func TestAFailedCloseOfThePreviousConfigurationIsLoggedAndForgotten(t *testing.T) {
	p := &tplinkProvider{current: &switchRegistry{def: &failingClose{}}}
	p.closeCurrent(t.Context())
	if p.current != nil {
		t.Fatal("the previous registry must be dropped even when closing it failed")
	}
	p.closeCurrent(t.Context()) // nothing to close the second time
}
