package provider

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

type terraformState = terraform.State

func expectLED(f *fakeClient, want bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.led != want {
		return fmt.Errorf("switch LED = %t, want %t", f.led, want)
	}
	return nil
}

func TestAddrString(t *testing.T) {
	if got := addrString(netip.MustParseAddr("192.0.2.13")); got != "192.0.2.13" {
		t.Errorf("addrString = %q", got)
	}
	if got := addrString(netip.Addr{}); got != "" {
		t.Errorf("an address the switch did not report must be empty, got %q", got)
	}
}
