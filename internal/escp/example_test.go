package escp_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
)

// The examples that talk to a switch have no Output comment, so they are compiled but never
// run: running them would send packets to a real network.

// Read a switch. Dial sends nothing; the first call opens the socket and does the key exchange.
func ExampleDial() {
	c, err := escp.Dial(escp.Config{Host: "192.0.2.11", Username: "admin", Password: "secret"})
	if err != nil {
		panic(err)
	}
	defer func() { _ = c.Close() }()

	ports, err := c.Ports(context.Background())
	if err != nil {
		panic(err)
	}
	for _, p := range ports {
		fmt.Printf("port %d: %s (configured %s)\n", p.ID, p.ActualSpeed, p.ConfiguredSpeed)
	}
}

// Write behind the firmware gate. A switch on a firmware the client was not verified
// against is read but never written: the write returns a *safety.FirmwareError and
// sends nothing.
func ExampleClient_SetLED() {
	c, err := escp.Dial(escp.Config{
		Host: "192.0.2.11", Username: "admin", Password: "secret",
		WriteGate: safety.FirmwareGate(),
	})
	if err != nil {
		panic(err)
	}
	defer func() { _ = c.Close() }()

	err = c.SetLED(context.Background(), false)
	var refused *safety.FirmwareError
	switch {
	case errors.As(err, &refused):
		fmt.Println("refused, firmware build", refused.Build)
	case errors.Is(err, escp.ErrRejected):
		fmt.Println("the switch refused the write:", err)
	case err != nil:
		fmt.Println("write failed:", err)
	}
}

// Find the switches on the network. Run it on its own: switches reply to UDP port 29809,
// which a Client or the vendor's utility on the same host would share out unpredictably.
func ExampleDiscover() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	switches, err := escp.Discover(ctx, escp.DiscoverOptions{Window: 2 * time.Second})
	if err != nil {
		fmt.Println("discovery stopped early:", err)
	}
	for _, s := range switches {
		fmt.Println(s.IP, s.MAC, s.Model, s.Firmware)
	}
}

// Name a switch by its MAC address: the client pins the identity (a device with another MAC is never
// written to) and finds the switch by itself when its address is not known or has changed.
func ExampleParseMAC() {
	mac, err := escp.ParseMAC("C0-3A-55-6C-81-61")
	fmt.Println(mac, err)
	_, err = escp.ParseMAC("c03a556c8161")
	fmt.Println(err)
	// Output:
	// c0:3a:55:6c:81:61 <nil>
	// escp: "c03a556c8161" is not a MAC address; write it as aa:bb:cc:dd:ee:ff (colons or hyphens)
}

// Find one switch by its MAC address, wherever it is on the segment. Like Discover, run it on its own.
func ExampleLocate() {
	mac, _ := escp.ParseMAC("c0:3a:55:6c:81:61")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, err := escp.Locate(ctx, mac, escp.DiscoverOptions{})
	if errors.Is(err, escp.ErrNotFound) {
		fmt.Println("no switch with that MAC answered")
		return
	}
	fmt.Println(info.IP, info.DHCP)
}

// Let the switch lease its address. The leased address is not known in advance, so the client must
// have the switch's MAC: it looks the switch up by it and follows it to the lease.
func ExampleClient_EnableDHCP() {
	mac, _ := escp.ParseMAC("c0:3a:55:6c:81:61")
	c, err := escp.Dial(escp.Config{MAC: mac, Username: "admin", Password: os.Getenv("SWITCH_PASSWORD")})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer c.Close()
	if err := c.EnableDHCP(context.Background()); err != nil {
		fmt.Println("DHCP not confirmed:", err) // ErrMoveUnconfirmed: look for the switch by its MAC before anything else
		return
	}
	// The way back is SetIPConfig with the static address: it writes the DHCP flag off.
}

func ExampleParseSpeed() {
	s, err := escp.ParseSpeed("100Mbps-full")
	fmt.Println(s, err)

	_, err = escp.ParseSpeed("link-down") // only ever reported, never configured
	fmt.Println(err != nil)
	// Output:
	// 100Mbps-full <nil>
	// true
}

func ExampleQoSModes() {
	for _, m := range escp.QoSModes() {
		fmt.Printf("%d %s\n", int(m), m)
	}
	// Output:
	// 0 port-based
	// 1 802.1p
	// 2 dscp-802.1p
}

func ExampleValidateIPConfig() {
	ok := escp.IPConfig{IP: net.IPv4(192, 168, 77, 11), Netmask: net.IPv4(255, 255, 255, 0), Gateway: net.IPv4(192, 168, 77, 1)}
	fmt.Println(escp.ValidateIPConfig(ok))

	bad := ok
	bad.Gateway = net.IPv4(192, 168, 78, 1) // another subnet: the switch would be unreachable
	fmt.Println(escp.ValidateIPConfig(bad) != nil)
	// Output:
	// <nil>
	// true
}

// A write the switch refuses matches ErrRejected, and carries the reply code.
func ExampleRejectedError() {
	err := fmt.Errorf("creating the group: %w", &escp.RejectedError{Code: 4})

	fmt.Println(errors.Is(err, escp.ErrRejected))
	var rejected *escp.RejectedError
	if errors.As(err, &rejected) {
		fmt.Println("code", rejected.Code)
	}
	// Output:
	// true
	// code 4
}
