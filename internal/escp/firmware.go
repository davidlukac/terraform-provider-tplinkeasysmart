package escp

import (
	"fmt"
	"regexp"
	"slices"
)

// VerifiedFirmwareBuilds are the firmware builds (the date in "1.0.0 Build
// 20230218 Rel.50633") whose protocol behaviour this client was tested
// against on real switches: reads, and writes on unused ports. Any other build
// may have changed layouts or authentication, so writes to it are refused
// unless the user opts in.
var VerifiedFirmwareBuilds = []string{"20230218", "20250710"}

var firmwareBuildPattern = regexp.MustCompile(`Build (\d{8})`)

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

// CheckFirmwareVerified returns an error unless the firmware version string
// names a build in VerifiedFirmwareBuilds.
func CheckFirmwareVerified(version string) error {
	build, ok := FirmwareBuild(version)
	if !ok {
		return fmt.Errorf("the firmware version %q has no build date, so it cannot be matched against the verified builds %v", version, VerifiedFirmwareBuilds)
	}
	if !slices.Contains(VerifiedFirmwareBuilds, build) {
		return fmt.Errorf("firmware build %s has not been verified with this provider version (verified: %v)", build, VerifiedFirmwareBuilds)
	}
	return nil
}
