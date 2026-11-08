package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidlukac/escp-go/model"
	"github.com/davidlukac/escp-go/safety"
)

// firmwareWriteGate returns the write gate of a switch client: the client
// refuses every write while the switch runs a firmware build the provider has
// not been verified against (safety.VerifiedFirmwareBuilds), unless the user
// opted in with allow_unverified_firmware, in which case there is no gate. Reads
// always pass: they cannot change the switch, and they are how a new firmware is
// fingerprinted. The refusal is worded here, in Terraform's vocabulary; the
// check itself and the guarantee that no write bypasses it live in the client.
func firmwareWriteGate(allowUnverified bool) model.WriteGate {
	return firmwareGate(allowUnverified, "write")
}

// firmwareGate is firmwareWriteGate with the action the refusal names ("write", "read the
// configuration file").
func firmwareGate(allowUnverified bool, action string) model.WriteGate {
	if allowUnverified {
		return nil
	}
	gate := safety.FirmwareGate()
	return func(ctx context.Context, sw model.Reader, op model.Operation) error {
		if err := gate(ctx, sw, op); err != nil {
			return refusal(err, action)
		}
		return nil
	}
}

// refusal words a firmware refusal for the user of the provider. A failed
// firmware read is reported as it is; the other refusals say how to opt in.
func refusal(err error, action string) error {
	var fe *safety.FirmwareError
	if !errors.As(err, &fe) {
		return fmt.Errorf("refusing to %s: %w", action, err)
	}
	detail := fe.Error()
	switch fe.Reason {
	case safety.FirmwareUnreadable:
		return fmt.Errorf("refusing to %s: %w", action, err)
	case safety.FirmwareNoBuildDate:
		// the library's wording already says what is missing
	case safety.FirmwareUnverified:
		detail = fmt.Sprintf("firmware build %s has not been verified with this provider version (verified: %v)", fe.Firmware.Build, fe.Verified)
	}
	return fmt.Errorf("refusing to %s: %s. Reads work on any firmware (the configuration file, which is read with a request the switch could take for a write on an unknown build, is the one exception); set allow_unverified_firmware = true (provider or switch) to %s anyway, at your own risk", action, detail, action)
}

// backupGated is a switch client whose configuration read passes the firmware gate. The library
// does not consult the write gate for ConfigBackup: it reads the file with a SET on the wire (the
// vendor way), which is not a write, so a read of the file would run on a firmware build the
// provider was not verified against, where that SET could mean something else. Every other read
// is a GET and passes without a check, as always.
type backupGated struct {
	SwitchClient
	gate model.WriteGate
}

// configBackupOperation is what the gate is told about the read of the configuration file: no
// method of the library (it is not a write), no port. Only the firmware gate looks at it.
var configBackupOperation = model.Operation{Name: "ConfigBackup", Impact: model.ImpactCosmetic}

// ConfigBackup reads the configuration file once the gate has accepted the switch's firmware.
func (b backupGated) ConfigBackup(ctx context.Context) (model.ConfigFile, error) {
	if err := b.gate(ctx, b.SwitchClient, configBackupOperation); err != nil {
		return model.ConfigFile{}, err
	}
	return b.SwitchClient.ConfigBackup(ctx)
}

// withBackupGate puts the firmware gate in front of the configuration read of c; without a gate
// (allow_unverified_firmware) c is returned as it is.
func withBackupGate(c SwitchClient, gate model.WriteGate) SwitchClient {
	if gate == nil {
		return c
	}
	return backupGated{SwitchClient: c, gate: gate}
}
