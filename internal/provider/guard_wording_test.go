package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/safety"
)

// The wording helper must not turn a refusal it does not know into "the ports are free".
func TestWordPortRefusal(t *testing.T) {
	word := func(used *safety.PortUseError) error { return errors.New("worded") }
	if err := wordPortRefusal(nil, word); err != nil {
		t.Fatalf("no refusal is no error, got %v", err)
	}
	if err := wordPortRefusal(&safety.PortUseError{Port: 4, Reason: safety.PortLinkUp}, word); err == nil || err.Error() != "worded" {
		t.Fatalf("a port refusal is worded by the guard, got %v", err)
	}
	unknown := errors.New("a refusal of another kind")
	if err := wordPortRefusal(unknown, word); !errors.Is(err, unknown) {
		t.Fatalf("an unknown refusal must come back as it is, got %v", err)
	}
	wrapped := errors.Join(errors.New("context"), &safety.PortUseError{Port: 2, Reason: safety.PortInLAG, LAG: 1})
	if err := wordPortRefusal(wrapped, word); err == nil || err.Error() != "worded" {
		t.Fatalf("a wrapped port refusal is still a port refusal, got %v", err)
	}
}

// A port number no switch has is not "free": the guard refuses it before it reads anything,
// even though a port set cannot hold it.
func TestGuardPortsRefusesAPortNumberNoSwitchHas(t *testing.T) {
	f := &fakeClient{ports: testPorts()}
	for _, bad := range []escp.PortID{0, 33, -4} {
		if err := guardPorts(context.Background(), f, false, 3, bad); err == nil {
			t.Errorf("port %d must be refused", bad)
		}
	}
	if err := guardPorts(context.Background(), f, true, 0); err != nil {
		t.Errorf("allow_disruptive skips the guard, as before: %v", err)
	}
}
