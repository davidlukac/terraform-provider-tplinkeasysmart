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
	err := safety.FirmwareGate()(context.Background(), firmware("1.0.0 Build 20230218 Rel.50633"))
	fmt.Println("verified build:", err)

	err = safety.FirmwareGate()(context.Background(), firmware("1.0.0 Build 20990101 Rel.1"))
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
	for _, id := range []int{1, 7, 9} {
		u := safety.UseOf(ports, id)
		fmt.Printf("port %d: exists=%v link=%v lag=%v\n", id, u.Exists, u.LinkUp(), u.InLAG())
	}
	// Output:
	// port 1: exists=true link=true lag=true
	// port 7: exists=true link=false lag=false
	// port 9: exists=false link=false lag=false
}

// CheckPorts names the first port a write should not touch; the caller words the refusal.
func ExampleCheckPorts() {
	ports := []escp.Port{
		{ID: 1, ActualSpeed: escp.Speed1000Full, LAG: 1},
		{ID: 2, ActualSpeed: escp.SpeedLinkDown, LAG: 1},
		{ID: 7, ActualSpeed: escp.SpeedLinkDown},
	}
	fmt.Println(safety.CheckPorts(ports, safety.PortRule{}, 7))
	err := safety.CheckPorts(ports, safety.PortRule{}, 7, 2)
	var used *safety.PortUseError
	if errors.As(err, &used) && used.Reason == safety.PortInLAG {
		fmt.Printf("port %d is in LAG %d\n", used.Port, used.LAG)
	}
	// Editing LAG 1 itself tolerates its idle members, but never a link.
	fmt.Println(safety.CheckPorts(ports, safety.PortRule{TolerateLAG: 1}, 2))
	fmt.Println(safety.CheckPorts(ports, safety.PortRule{TolerateLAG: 1}, 1))
	// Output:
	// <nil>
	// port 2 is in LAG 1
	// <nil>
	// port 1 has a link up (1000Mbps-full)
}
