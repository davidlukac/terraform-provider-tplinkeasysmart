package escp_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
)

// The gates of the safety package, composed as a front end would, against the real client and the
// fake switch: the firmware gate, a ceiling on the impact, and a dry run that records what would
// have been written.
func TestDryRunThroughTheRealClient(t *testing.T) {
	f := escptest.Start(t)
	dry := safety.NewDryRun()
	c, err := escp.Dial(escp.Config{
		Host: "127.0.0.1", Username: escptest.Username, Password: escptest.Password,
		Timeout: 300 * time.Millisecond, SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: "127.0.0.1",
		WriteGate: safety.Chain(safety.FirmwareGate(), safety.MaxImpact(escp.ImpactTraffic), dry.Gate()),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	// writes within the ceiling are recorded and refused with ErrDryRun, which means success here
	if err := c.SetPort(ctx, escp.PortConfig{ID: 3, Enabled: true, Speed: escp.SpeedAuto}); !errors.Is(err, safety.ErrDryRun) {
		t.Fatalf("SetPort: got %v, want ErrDryRun", err)
	}
	if err := c.SetBandwidth(ctx, escp.PortBandwidth{Port: 4, IngressKbps: 64}); !errors.Is(err, safety.ErrDryRun) {
		t.Fatalf("SetBandwidth: got %v, want ErrDryRun", err)
	}
	if err := c.SetLED(ctx, false); !errors.Is(err, safety.ErrDryRun) {
		t.Fatalf("SetLED: got %v, want ErrDryRun", err)
	}
	// a write above the ceiling is refused before the dry run sees it
	var tooMuch *safety.ImpactError
	if err := c.SetLAG(ctx, 1, []int{2, 3}); !errors.As(err, &tooMuch) || tooMuch.Op.Name != "SetLAG" {
		t.Fatalf("SetLAG: got %v, want an *ImpactError for SetLAG", err)
	}

	want := []escp.Operation{
		{Name: "SetPort", Ports: []int{3}, Impact: escp.ImpactTraffic},
		{Name: "SetBandwidth", Ports: []int{4}, Impact: escp.ImpactTraffic}, // two SETs, one operation: the first refusal ends it
		{Name: "SetLED", Impact: escp.ImpactCosmetic},
	}
	if got := dry.Operations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded %+v, want %+v", got, want)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("a dry run must send nothing, the switch received %d writes", n)
	}
}

// A write that first reads from the switch still reads, and still sends nothing.
func TestDryRunOfAWriteThatReadsFirst(t *testing.T) {
	f := escptest.Start(t)
	dry := safety.NewDryRun()
	c, err := escp.Dial(escp.Config{
		Host: "127.0.0.1", Username: escptest.Username, Password: escptest.Password,
		Timeout: 300 * time.Millisecond, SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: "127.0.0.1",
		WriteGate: dry.Gate(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetPortPriorities(context.Background(), map[int]escp.Priority{5: escp.PriorityHighest, 2: escp.PriorityLowest}); !errors.Is(err, safety.ErrDryRun) {
		t.Fatalf("got %v, want ErrDryRun", err)
	}
	want := []escp.Operation{{Name: "SetPortPriorities", Ports: []int{2, 5}, Impact: escp.ImpactTraffic}}
	if got := dry.Operations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded %+v, want %+v", got, want)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("the switch received %d writes", n)
	}
}
