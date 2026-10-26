package safety

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sync"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// verifiedFirmwareBuilds is the allow-list behind VerifiedFirmwareBuilds: the builds of the
// verified rows of the compatibility table (compatibility.json). It is unexported so that no
// code in the process can append to it and weaken the gate.
var verifiedFirmwareBuilds = verifiedBuilds(compatibility)

// VerifiedFirmwareBuilds returns the firmware builds (the date in "1.0.0 Build
// 20230218 Rel.50633") whose protocol behaviour this client was tested
// against on real switches: reads, and writes on unused ports. Any other build
// may have changed layouts or authentication, so writes to it are refused
// unless the user opts in. The result is a copy: changing it changes nothing.
func VerifiedFirmwareBuilds() []string { return slices.Clone(verifiedFirmwareBuilds) }

var firmwareBuildPattern = regexp.MustCompile(`Build (\d{8})\b`)

// FirmwareBuild extracts the build date from a firmware version string as the
// switch reports it ("1.0.0 Build 20230218 Rel.50633"). ok is false when the
// string has no build date.
func FirmwareBuild(version string) (build string, ok bool) {
	m := firmwareBuildPattern.FindStringSubmatch(version)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// FirmwareReason says why a firmware check refused a write.
type FirmwareReason int

// The reasons a firmware check refuses.
const (
	// FirmwareUnreadable: the firmware version could not be read from the switch.
	FirmwareUnreadable FirmwareReason = iota + 1
	// FirmwareNoBuildDate: the version string has no build date to match.
	FirmwareNoBuildDate
	// FirmwareUnverified: the build is not in VerifiedFirmwareBuilds.
	FirmwareUnverified
)

// FirmwareError is the refusal of a firmware check. Callers branch on Reason and
// word the message for their users; Error is a neutral description.
type FirmwareError struct {
	Reason   FirmwareReason
	Version  string   // the version string the switch reported (not for FirmwareUnreadable)
	Build    string   // the build date (FirmwareUnverified)
	Verified []string // the builds that would have been accepted
	Err      error    // the cause (FirmwareUnreadable)
}

// Error implements error.
func (e *FirmwareError) Error() string {
	switch e.Reason {
	case FirmwareUnreadable:
		return fmt.Sprintf("cannot read the firmware version to check it: %v", e.Err)
	case FirmwareNoBuildDate:
		return fmt.Sprintf("the firmware version %q has no build date, so it cannot be matched against the verified builds %v", e.Version, e.Verified)
	default:
		return fmt.Sprintf("firmware build %s has not been verified with this version of the client (verified: %v)", e.Build, e.Verified)
	}
}

// Unwrap returns the cause of a FirmwareUnreadable refusal.
func (e *FirmwareError) Unwrap() error { return e.Err }

// CheckFirmware returns a *FirmwareError unless the firmware version string names
// a build in VerifiedFirmwareBuilds.
func CheckFirmware(version string) error {
	build, ok := FirmwareBuild(version)
	if !ok {
		return &FirmwareError{Reason: FirmwareNoBuildDate, Version: version, Verified: VerifiedFirmwareBuilds()}
	}
	if !slices.Contains(verifiedFirmwareBuilds, build) {
		return &FirmwareError{Reason: FirmwareUnverified, Version: version, Build: build, Verified: VerifiedFirmwareBuilds()}
	}
	return nil
}

// FirmwareGate returns an escp.WriteGate that refuses every write while the
// switch runs a firmware build the client was not verified against. Reads always
// pass: they cannot change the switch, and they are how a new firmware is
// fingerprinted. The firmware is read before the first write; a verified build is
// remembered, a refusal or a failed read is not (the next write checks again). The
// gate belongs to one switch: use one gate per client.
func FirmwareGate() escp.WriteGate {
	g := &firmwareGate{}
	return g.check
}

type firmwareGate struct {
	mu       sync.Mutex
	verified bool
}

func (g *firmwareGate) check(ctx context.Context, sw escp.InfoReader, _ escp.Operation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.verified {
		return nil
	}
	info, err := sw.Info(ctx)
	if err != nil {
		return &FirmwareError{Reason: FirmwareUnreadable, Err: err}
	}
	if err := CheckFirmware(info.Firmware); err != nil {
		return err
	}
	g.verified = true
	return nil
}
