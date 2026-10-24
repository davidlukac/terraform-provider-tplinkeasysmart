package provider

import (
	"errors"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
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
