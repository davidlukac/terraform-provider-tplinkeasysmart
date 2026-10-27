package safety

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

func TestCheckFirmware(t *testing.T) {
	for _, v := range []string{"1.0.0 Build 20230218 Rel.50633", "1.0.0 Build 20250710 Rel.71093"} {
		if err := CheckFirmware(escp.ParseFirmware(v)); err != nil {
			t.Errorf("%q must be verified: %v", v, err)
		}
	}
	tests := []struct {
		version, frag string
		reason        FirmwareReason
		build         string
	}{
		{"1.0.0 Build 20260101 Rel.1", "build 20260101 has not been verified", FirmwareUnverified, "20260101"},
		{"1.0.0 Build 20220930 Rel.1", "build 20220930 has not been verified", FirmwareUnverified, "20220930"},
		{"unknown", "has no build date", FirmwareNoBuildDate, ""},
		{"", "has no build date", FirmwareNoBuildDate, ""},
	}
	for _, tt := range tests {
		err := CheckFirmware(escp.ParseFirmware(tt.version))
		var fe *FirmwareError
		if !errors.As(err, &fe) || fe.Reason != tt.reason || fe.Build != tt.build || fe.Version != tt.version || !strings.Contains(err.Error(), tt.frag) {
			t.Errorf("CheckFirmware(%q) = %#v, want reason %d build %q containing %q", tt.version, err, tt.reason, tt.build, tt.frag)
		}
		if !slices.Equal(fe.Verified, VerifiedFirmwareBuilds()) {
			t.Errorf("CheckFirmware(%q) lists %v, want %v", tt.version, fe.Verified, VerifiedFirmwareBuilds())
		}
	}
}

// The verified list in an error is a copy: a caller cannot change the allow-list through it.
func TestFirmwareErrorDoesNotExposeTheAllowList(t *testing.T) {
	var fe *FirmwareError
	if !errors.As(CheckFirmware(escp.ParseFirmware("1.0.0 Build 20990101 Rel.1")), &fe) {
		t.Fatal("want a FirmwareError")
	}
	fe.Verified[0] = "tampered"
	if VerifiedFirmwareBuilds()[0] == "tampered" {
		t.Fatal("the allow-list was modified through the error")
	}
}

// The accessor hands out a copy too, and the allow-list is what the gate decides by.
func TestVerifiedFirmwareBuildsIsACopy(t *testing.T) {
	got := VerifiedFirmwareBuilds()
	if !slices.Contains(got, "20230218") || !slices.Contains(got, "20250710") {
		t.Fatalf("the two builds tested on real switches must be listed, got %v", got)
	}
	got[0] = "20990101" // overwrite an entry of the copy with a build nobody verified
	if err := CheckFirmware(escp.ParseFirmware("1.0.0 Build 20990101 Rel.1")); err == nil {
		t.Error("a build written into the copy passed the gate")
	}
	if err := CheckFirmware(escp.ParseFirmware("1.0.0 Build 20230218 Rel.50633")); err != nil {
		t.Errorf("a verified build was refused after the copy was changed: %v", err)
	}
}

// infoSource is a switch that reports a firmware version, or fails to.
type infoSource struct {
	firmware string
	err      error
	reads    int
}

func (s *infoSource) Info(context.Context) (escp.Info, error) {
	s.reads++
	return escp.Info{Firmware: escp.ParseFirmware(s.firmware)}, s.err
}

func TestFirmwareGateAllowsVerifiedBuildsAndReadsTheFirmwareOnce(t *testing.T) {
	for _, version := range []string{"1.0.0 Build 20230218 Rel.50633", "1.0.0 Build 20250710 Rel.71093"} {
		sw := &infoSource{firmware: version}
		gate := FirmwareGate()
		for i := 0; i < 3; i++ {
			if err := gate(context.Background(), sw, escp.Operation{}); err != nil {
				t.Fatalf("%s: %v", version, err)
			}
		}
		if sw.reads != 1 {
			t.Fatalf("%s: want one firmware read for three writes, got %d", version, sw.reads)
		}
	}
}

func TestFirmwareGateRefusesAnUnverifiedBuildAndDoesNotRememberTheRefusal(t *testing.T) {
	sw := &infoSource{firmware: "1.0.0 Build 20990101 Rel.1"}
	gate := FirmwareGate()
	var fe *FirmwareError
	if err := gate(context.Background(), sw, escp.Operation{}); !errors.As(err, &fe) || fe.Reason != FirmwareUnverified || fe.Build != "20990101" {
		t.Fatalf("got %v", err)
	}
	sw.firmware = "1.0.0 Build 20230218 Rel.50633" // the switch now reports a verified build
	if err := gate(context.Background(), sw, escp.Operation{}); err != nil {
		t.Fatalf("a refusal must not stick: %v", err)
	}
}

func TestFirmwareGateRefusesWhenTheFirmwareCannotBeRead(t *testing.T) {
	cause := errors.New("no reply")
	sw := &infoSource{firmware: "1.0.0 Build 20230218 Rel.50633", err: cause}
	gate := FirmwareGate()
	var fe *FirmwareError
	err := gate(context.Background(), sw, escp.Operation{})
	if !errors.As(err, &fe) || fe.Reason != FirmwareUnreadable || !errors.Is(err, cause) || !strings.Contains(err.Error(), "cannot read the firmware version") {
		t.Fatalf("got %v", err)
	}
	sw.err = nil
	if err := gate(context.Background(), sw, escp.Operation{}); err != nil {
		t.Fatalf("the check is retried once the switch answers: %v", err)
	}
}

func TestFirmwareGateRefusesAFirmwareWithoutABuildDate(t *testing.T) {
	var fe *FirmwareError
	if err := FirmwareGate()(context.Background(), &infoSource{firmware: "custom"}, escp.Operation{}); !errors.As(err, &fe) || fe.Reason != FirmwareNoBuildDate {
		t.Fatalf("got %v", err)
	}
}

// Each gate owns its verdict: a verified switch must not vouch for another one.
func TestFirmwareGatesDoNotShareState(t *testing.T) {
	good := &infoSource{firmware: "1.0.0 Build 20230218 Rel.50633"}
	bad := &infoSource{firmware: "1.0.0 Build 20990101 Rel.1"}
	a, b := FirmwareGate(), FirmwareGate()
	if err := a(context.Background(), good, escp.Operation{}); err != nil {
		t.Fatal(err)
	}
	if err := b(context.Background(), bad, escp.Operation{}); err == nil {
		t.Fatal("the second gate must check its own switch")
	}
}
