package escp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

// Half duplex on a port that is a LAG member was refused with code 7407 on a real switch
// (switch 03, firmware 20250710, ports 3 and 4 in LAG1); the error must say why.
func TestSetPortRefusalNamesTheLAGDuplexRule(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom, f.RejectCode = 1, lagRefusedHalfDuplex })
	c := dialFast(t, f, "secret")
	err := c.SetPort(context.Background(), PortConfig{ID: 3, Enabled: true, Speed: Speed100Half})
	var rejected *RejectedError
	if !errors.Is(err, ErrRejected) || !errors.As(err, &rejected) || rejected.Code != 7407 {
		t.Fatalf("got %v, want a RejectedError with code 7407 that matches ErrRejected", err)
	}
	if !strings.Contains(err.Error(), "member of a LAG") || !strings.Contains(err.Error(), "full duplex") || strings.Contains(err.Error(), "wrong credentials") {
		t.Fatalf("message %q must name the LAG duplex rule and not blame the credentials", err)
	}
}

// Only the code that was provoked is named; any other refusal keeps the generic message, and
// an accepted write is unaffected.
func TestSetPortRefusalOfAnotherCodeStaysGeneric(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom, f.RejectCode = 1, 7406 })
	c := dialFast(t, f, "secret")
	err := c.SetPort(context.Background(), PortConfig{ID: 3, Enabled: true, Speed: SpeedAuto})
	if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "error code 7406") || !strings.Contains(err.Error(), "wrong credentials") {
		t.Fatalf("got %v", err)
	}
	g := escptest.Start(t)
	if err := dialFast(t, g, "secret").SetPort(context.Background(), PortConfig{ID: 3, Enabled: true, Speed: SpeedAuto}); err != nil {
		t.Fatalf("an accepted write must stay accepted: %v", err)
	}
}
