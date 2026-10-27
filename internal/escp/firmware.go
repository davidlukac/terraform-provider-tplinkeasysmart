package escp

import "regexp"

// Firmware is the version string a switch reports ("1.0.0 Build 20230218 Rel.50633"), with the one
// part anything matches on, the build date, read out of it once. It is a comparable value: two are
// equal when the switch reported the same string. The zero value is "no firmware reported".
type Firmware struct {
	// Build is the eight-digit build date ("20230218"); empty when the string has no build that
	// stands alone ("Build 2025071012" is not a build date).
	Build string
	raw   string
}

var firmwareBuildPattern = regexp.MustCompile(`Build (\d{8})\b`)

// ParseFirmware reads a version string. It never fails: a string that does not look like a vendor
// version has an empty Build (and so matches nothing that needs one), and String still returns it
// as it came.
func ParseFirmware(s string) Firmware {
	f := Firmware{raw: s}
	if m := firmwareBuildPattern.FindStringSubmatch(s); m != nil {
		f.Build = m[1]
	}
	return f
}

// String is the version string as the switch reported it.
func (f Firmware) String() string { return f.raw }

// IsZero reports whether the switch reported no firmware version.
func (f Firmware) IsZero() bool { return f.raw == "" }

// HasBuild reports whether the string names a build date.
func (f Firmware) HasBuild() bool { return f.Build != "" }
