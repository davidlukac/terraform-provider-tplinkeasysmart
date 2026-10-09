package provider

import (
	"fmt"

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
