package escp_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
)

// The gates of the safety package, composed as a front end would, against the real client and the
// fake switch: the firmware gate, a ceiling on the impact, and a dry run that records what would
// have been written.
func TestDryRunThroughTheRealClient(t *testing.T) {
	f := escptest.Start(t)
	f.EnableModel(nil) // the pre-write read of the other limiter needs a switch that answers
	dry := safety.NewDryRun()
	c, err := escp.Dial(escp.Config{
		Host: "127.0.0.1", Username: escptest.Username, Password: escptest.Password,
		Timeout: 300 * time.Millisecond, SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: escptest.Loopback,
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
	if err := c.SetLAG(ctx, 1, escp.MustPortSet(2, 3)); !errors.As(err, &tooMuch) || tooMuch.Op.Name != "SetLAG" {
		t.Fatalf("SetLAG: got %v, want an *ImpactError for SetLAG", err)
	}

	want := []escp.Operation{
		{Name: "SetPort", Ports: escp.MustPortSet(3), Impact: escp.ImpactTraffic},
		{Name: "SetBandwidth", Ports: escp.MustPortSet(4), Impact: escp.ImpactTraffic}, // two SETs, one operation: the first refusal ends it
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
		Timeout: 300 * time.Millisecond, SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: escptest.Loopback,
		WriteGate: dry.Gate(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if err := c.SetPortPriorities(context.Background(), map[escp.PortID]escp.Priority{5: escp.PriorityHighest, 2: escp.PriorityLowest}); !errors.Is(err, safety.ErrDryRun) {
		t.Fatalf("got %v, want ErrDryRun", err)
	}
	want := []escp.Operation{{Name: "SetPortPriorities", Ports: escp.MustPortSet(2, 5), Impact: escp.ImpactTraffic}}
	if got := dry.Operations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recorded %+v, want %+v", got, want)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("the switch received %d writes", n)
	}
}

// PortGate through the real client: the uplink cannot be touched, named or not, and a free port can.
func TestPortGateThroughTheRealClient(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.GetFn = func(types []wire.TLVType) ([]escptest.Setting, int32, bool) {
			var out []escptest.Setting
			for _, typ := range types {
				switch typ {
				case wire.TLVPorts: // port 1 is the linked uplink in LAG 1, port 3 is free
					out = append(out,
						escptest.Setting{Type: wire.TLVPorts, Value: []byte{1, 1, 1, 1, 6, 0, 0}},
						escptest.Setting{Type: wire.TLVPorts, Value: []byte{3, 1, 0, 1, 0, 0, 0}})
				case wire.TLVLAG: // LAG 1 = ports 1 and 2
					out = append(out, escptest.Setting{Type: wire.TLVLAG, Value: []byte{1, 0, 0, 0, 3}})
				case wire.TLVPortCount:
					out = append(out, escptest.Setting{Type: wire.TLVPortCount, Value: []byte{8}})
				}
			}
			return out, 0, false
		}
	})
	c, err := escp.Dial(escp.Config{
		Host: "127.0.0.1", Username: escptest.Username, Password: escptest.Password,
		Timeout: 300 * time.Millisecond, SwitchPort: f.Port(), HostPort: escptest.FreeUDPPort(t), BroadcastAddr: escptest.Loopback,
		WriteGate: safety.PortGate(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()

	var used *safety.PortUseError
	if err := c.SetPort(ctx, escp.PortConfig{ID: 1, Enabled: false, Speed: escp.SpeedAuto}); !errors.As(err, &used) || used.Port != 1 || used.Reason != safety.PortLinkUp {
		t.Fatalf("disabling the uplink: got %v, want a link-up refusal for port 1", err)
	}
	// removing LAG 1 names no port, yet its member carries the link
	if err := c.SetLAG(ctx, 1, 0); !errors.As(err, &used) || used.Port != 1 {
		t.Fatalf("removing LAG 1: got %v, want a refusal for port 1", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("the refusals must send nothing, the switch received %d writes", n)
	}
	if err := c.SetPort(ctx, escp.PortConfig{ID: 3, Enabled: true, Speed: escp.SpeedAuto}); err != nil {
		t.Fatalf("a free port: %v", err)
	}
	if n := f.WriteCount(); n != 1 {
		t.Fatalf("the free port is written once, got %d", n)
	}
}
