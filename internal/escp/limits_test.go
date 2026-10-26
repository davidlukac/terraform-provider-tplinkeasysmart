package escp

import (
	"errors"
	"testing"
)

func TestKbpsValidate(t *testing.T) {
	for _, k := range []Kbps{0, 64, 128, 1024, MaxBandwidthKbps} {
		if err := k.Validate(); err != nil {
			t.Errorf("%d kbps: %v", int(k), err)
		}
	}
	// Not a multiple of the step, negative, and above the largest limit the web UI offers.
	for _, k := range []Kbps{-64, -1, 1, 63, 100, MaxBandwidthKbps + 64, 2000000} {
		if err := k.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%d kbps: got %v, want ErrInvalid", int(k), err)
		}
	}
	if Kbps(128).String() != "128 kbps" {
		t.Errorf("a rate prints with its unit, got %q", Kbps(128).String())
	}
}
