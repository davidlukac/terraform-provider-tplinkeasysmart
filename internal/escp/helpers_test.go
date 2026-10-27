package escp

import (
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

// newBoundClient dials a client on a free local port and binds its socket
// immediately, retrying with another port if something grabbed the first one
// in the meantime. Once it returns, the port is held by the client, so two
// tests can never end up on the same port.
func newBoundClient(t testing.TB, cfg Config) *Client {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		cfg.HostPort = escptest.FreeUDPPort(t)
		c, err := Dial(cfg)
		if err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		lastErr = c.open()
		c.mu.Unlock()
		if lastErr == nil {
			return c
		}
	}
	t.Fatalf("no free UDP port after 20 attempts: %v", lastErr)
	return nil
}

func dialFake(t *testing.T, f *escptest.Switch, pass string) *Client {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: escptest.Loopback})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	return c
}

// dialFast is dialFake with short timeouts and a single retry, for tests that
// deliberately make the switch stay silent.
func dialFast(t *testing.T, f *escptest.Switch, pass string) *Client {
	t.Helper()
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 80 * time.Millisecond, Retries: 1,
		SwitchPort: f.Port(), BroadcastAddr: escptest.Loopback})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	return c
}
