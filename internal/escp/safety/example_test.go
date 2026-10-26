package safety_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
)

// firmware is an escp.InfoReader that reports a fixed firmware version.
type firmware string

func (f firmware) Info(context.Context) (escp.Info, error) {
	return escp.Info{Firmware: string(f)}, nil
}

// A gate belongs to one switch: it remembers a verified build, so each switch gets its own.
func ExampleFirmwareGate() {
	err := safety.FirmwareGate()(context.Background(), firmware("1.0.0 Build 20230218 Rel.50633"), escp.Operation{})
	fmt.Println("verified build:", err)

	err = safety.FirmwareGate()(context.Background(), firmware("1.0.0 Build 20990101 Rel.1"), escp.Operation{})
	var refused *safety.FirmwareError
	if errors.As(err, &refused) && refused.Reason == safety.FirmwareUnverified {
		fmt.Println("refused build", refused.Build, "- verified:", refused.Verified)
	}
	// Output:
	// verified build: <nil>
	// refused build 20990101 - verified: [20230218 20250710]
}

func ExampleFirmwareBuild() {
	build, ok := safety.FirmwareBuild("1.0.0 Build 20250710 Rel.71093")
	fmt.Println(build, ok)
	_, ok = safety.FirmwareBuild("custom")
	fmt.Println(ok)
	// Output:
	// 20250710 true
	// false
}

// UseOf states facts about a port; the caller decides what they mean for its write.
func ExampleUseOf() {
	ports := []escp.Port{
		{ID: 1, ActualSpeed: escp.Speed1000Full, LAG: 1},
		{ID: 7, ActualSpeed: escp.SpeedLinkDown},
	}
	for _, id := range []escp.PortID{1, 7, 9} {
		u := safety.UseOf(ports, id)
		fmt.Printf("port %d: exists=%v link=%v lag=%v\n", id, u.Exists, u.LinkUp(), u.InLAG())
	}
	// Output:
	// port 1: exists=true link=true lag=true
	// port 7: exists=true link=false lag=false
	// port 9: exists=false link=false lag=false
}

// CheckPorts names the lowest-numbered port a write should not touch; the caller words the refusal.
func ExampleCheckPorts() {
	ports := []escp.Port{
		{ID: 1, ActualSpeed: escp.Speed1000Full, LAG: 1},
		{ID: 2, ActualSpeed: escp.SpeedLinkDown, LAG: 1},
		{ID: 7, ActualSpeed: escp.SpeedLinkDown},
	}
	fmt.Println(safety.CheckPorts(ports, safety.PortRule{}, escp.MustPortSet(7)))
	err := safety.CheckPorts(ports, safety.PortRule{}, escp.MustPortSet(7, 2))
	var used *safety.PortUseError
	if errors.As(err, &used) && used.Reason == safety.PortInLAG {
		fmt.Printf("port %d is in LAG %d\n", used.Port, used.LAG)
	}
	// Editing LAG 1 itself tolerates its idle members, but never a link.
	fmt.Println(safety.CheckPorts(ports, safety.PortRule{TolerateLAG: 1}, escp.MustPortSet(2)))
	fmt.Println(safety.CheckPorts(ports, safety.PortRule{TolerateLAG: 1}, escp.MustPortSet(1)))
	// Output:
	// <nil>
	// port 2 is in LAG 1
	// <nil>
	// port 1 has a link up (1000Mbps-full)
}

// A dry run records what a front end would write. The gates are chained so that the dry run only
// sees what the others allowed; the client returns ErrDryRun for every write and sends nothing.
func ExampleDryRun() {
	dry := safety.NewDryRun()
	gate := safety.Chain(safety.MaxImpact(escp.ImpactTraffic), dry.Gate())
	ctx := context.Background()

	for _, op := range []escp.Operation{
		{Name: "SetLED", Impact: escp.ImpactCosmetic},
		{Name: "SetPort", Ports: escp.MustPortSet(7), Impact: escp.ImpactTraffic},
		{Name: "SetLAG", Ports: escp.MustPortSet(5, 6), Impact: escp.ImpactManagement},
	} {
		err := gate(ctx, firmware(""), op)
		var tooMuch *safety.ImpactError
		switch {
		case errors.Is(err, safety.ErrDryRun):
			fmt.Println("would write:", op.Name)
		case errors.As(err, &tooMuch):
			fmt.Println("refused:", tooMuch)
		}
	}
	fmt.Println(len(dry.Operations()), "recorded")
	// Output:
	// would write: SetLED
	// would write: SetPort
	// refused: SetLAG has impact management, above the allowed traffic
	// 2 recorded
}
