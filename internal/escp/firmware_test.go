package escp

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseFirmware(t *testing.T) {
	tests := []struct {
		version string
		want    BuildDate
		ok      bool
	}{
		{"1.0.0 Build 20230218 Rel.50633", "20230218", true}, // switch 01, as reported by discovery
		{"1.0.0 Build 20250710 Rel.71093", "20250710", true}, // switch 03
		{"1.0.0 Build 20230218", "20230218", true},
		{"1.0.0", "", false},
		{"", "", false},
		{"Build 2023021", "", false},                // seven digits is not a date
		{"1.0.0 Build 2025071012 Rel.1", "", false}, // ten digits are not a verified date with a tail
		{"1.0.0 Build 202302189 Rel.1", "", false},
		{"1.0.0 Build 20230218Rel.50633", "", false}, // glued to a letter is not the format the switch reports
		{"1.0.0 Build 20230218.", "20230218", true},
	}
	for _, tt := range tests {
		got := ParseFirmware(tt.version)
		if got.Build != tt.want || got.HasBuild() != tt.ok || got.String() != tt.version {
			t.Errorf("ParseFirmware(%q) = build %q, has %t, string %q; want build %q, has %t, the string as it came", tt.version, got.Build, got.HasBuild(), got.String(), tt.want, tt.ok)
		}
	}
}

func TestFirmwareIsAComparableValue(t *testing.T) {
	a, b := ParseFirmware("1.0.0 Build 20230218 Rel.50633"), ParseFirmware("1.0.0 Build 20230218 Rel.50633")
	if a != b {
		t.Fatal("the same string is the same firmware")
	}
	if a == ParseFirmware("1.0.0 Build 20230218 Rel.50634") {
		t.Fatal("another release is another firmware, although the build date is the same")
	}
	if !(Firmware{}).IsZero() || a.IsZero() {
		t.Fatal("IsZero is true only for no firmware at all")
	}
}

func TestParseBuildDate(t *testing.T) {
	if got, err := ParseBuildDate("20230218"); err != nil || got != "20230218" || got.String() != "20230218" {
		t.Errorf("ParseBuildDate = %q, %v", got, err)
	}
	for _, in := range []string{"", "2023021", "202302189", "2025-07", "2023021a", " 20230218"} {
		if _, err := ParseBuildDate(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseBuildDate(%q): got %v, want ErrInvalid", in, err)
		}
	}
}

func TestBuildDateReadsFromJSON(t *testing.T) {
	var rows struct {
		Builds []BuildDate `json:"builds"`
	}
	if err := json.Unmarshal([]byte(`{"builds":["20230218","20250710"]}`), &rows); err != nil || len(rows.Builds) != 2 || rows.Builds[1] != "20250710" {
		t.Fatalf("got %v, %v", rows.Builds, err)
	}
	if err := json.Unmarshal([]byte(`{"builds":["2025-07"]}`), &rows); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a malformed build date must be refused: %v", err)
	}
}
