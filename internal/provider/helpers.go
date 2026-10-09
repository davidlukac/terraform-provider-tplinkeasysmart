package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// clientFrom extracts the switch client from provider data (nil before the
// provider is configured).
func clientFrom(data any, diags *diag.Diagnostics) SwitchClient {
	if data == nil {
		return nil
	}
	c, ok := data.(SwitchClient)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", data))
		return nil
	}
	return c
}
