package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/safety"
)

// firmwareWriteGate returns the write gate of a switch client: the client
// refuses every write while the switch runs a firmware build the provider has
// not been verified against (safety.VerifiedFirmwareBuilds), unless the user
// opted in with allow_unverified_firmware, in which case there is no gate. Reads
// always pass: they cannot change the switch, and they are how a new firmware is
// fingerprinted. The refusal is worded here, in Terraform's vocabulary; the
// check itself and the guarantee that no write bypasses it live in the client.
func firmwareWriteGate(allowUnverified bool) escp.WriteGate {
	if allowUnverified {
		return nil
	}
	gate := safety.FirmwareGate()
	return func(ctx context.Context, sw escp.Reader, op escp.Operation) error {
		if err := gate(ctx, sw, op); err != nil {
			return writeRefusal(err)
		}
		return nil
	}
}

// writeRefusal words a firmware refusal for the user of the provider. A failed
// firmware read is reported as it is; the other refusals say how to opt in.
func writeRefusal(err error) error {
	var fe *safety.FirmwareError
	if !errors.As(err, &fe) {
		return fmt.Errorf("refusing to write: %w", err)
	}
	detail := fe.Error()
	switch fe.Reason {
	case safety.FirmwareUnreadable:
		return fmt.Errorf("refusing to write: %w", err)
	case safety.FirmwareNoBuildDate:
		// the library's wording already says what is missing
	case safety.FirmwareUnverified:
		detail = fmt.Sprintf("firmware build %s has not been verified with this provider version (verified: %v)", fe.Firmware.Build, fe.Verified)
	}
	return fmt.Errorf("refusing to write: %s. Reads work on any firmware; set allow_unverified_firmware = true (provider or switch) to write anyway, at your own risk", detail)
}
