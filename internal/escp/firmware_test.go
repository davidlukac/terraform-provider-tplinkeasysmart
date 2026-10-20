package escp

import (
	"strings"
	"testing"
)

func TestFirmwareBuild(t *testing.T) {
	tests := []struct {
		version string
		want    string
		ok      bool
	}{
		{"1.0.0 Build 20230218 Rel.50633", "20230218", true}, // switch 01, as reported by discovery
		{"1.0.0 Build 20250710 Rel.71093", "20250710", true}, // switch 03
		{"1.0.0 Build 20230218", "20230218", true},
		{"1.0.0", "", false},
		{"", "", false},
		{"Build 2023021", "", false}, // seven digits is not a date
	}
	for _, tt := range tests {
		got, ok := FirmwareBuild(tt.version)
		if got != tt.want || ok != tt.ok {
			t.Errorf("FirmwareBuild(%q) = %q, %t; want %q, %t", tt.version, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCheckFirmwareVerified(t *testing.T) {
	for _, v := range []string{"1.0.0 Build 20230218 Rel.50633", "1.0.0 Build 20250710 Rel.71093"} {
		if err := CheckFirmwareVerified(v); err != nil {
			t.Errorf("%q must be verified: %v", v, err)
		}
	}
	tests := []struct{ version, frag string }{
		{"1.0.0 Build 20260101 Rel.1", "build 20260101 has not been verified"},
		{"1.0.0 Build 20220930 Rel.1", "build 20220930 has not been verified"},
		{"unknown", "has no build date"},
		{"", "has no build date"},
	}
	for _, tt := range tests {
		err := CheckFirmwareVerified(tt.version)
		if err == nil || !strings.Contains(err.Error(), tt.frag) {
			t.Errorf("CheckFirmwareVerified(%q) = %v, want an error containing %q", tt.version, err, tt.frag)
		}
	}
}
