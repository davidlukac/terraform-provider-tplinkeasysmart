package escp

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

var errGateRefused = errors.New("test gate: refused")

// writeCalls maps every method of Client that changes the switch to a call with
// valid arguments, so the call can only fail because the gate says no. The test
// below finds the write methods by reflection and fails when one is missing from
// this table, so a new Set* or Delete* method cannot skip the gate unnoticed.
var writeCalls = map[string]func(context.Context, *Client) error{
	"SetLED": func(ctx context.Context, c *Client) error { return c.SetLED(ctx, false) },
	"SetBandwidth": func(ctx context.Context, c *Client) error {
		return c.SetBandwidth(ctx, PortBandwidth{Port: 2, IngressKbps: 64, EgressKbps: 64})
	},
	"SetIGMP": func(ctx context.Context, c *Client) error {
		return c.SetIGMP(ctx, IGMPConfig{Enabled: true, ReportSuppression: false})
	},
	"SetLAG":            func(ctx context.Context, c *Client) error { return c.SetLAG(ctx, 1, []int{2, 3}) },
	"SetLoopPrevention": func(ctx context.Context, c *Client) error { return c.SetLoopPrevention(ctx, true) },
	"SetPort": func(ctx context.Context, c *Client) error {
		return c.SetPort(ctx, PortConfig{ID: 2, Enabled: true, Speed: "auto", FlowControl: false})
	},
	"SetPortMirror": func(ctx context.Context, c *Client) error {
		return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 2, IngressPorts: []int{3}})
	},
	"SetQoSMode":        func(ctx context.Context, c *Client) error { return c.SetQoSMode(ctx, 1) },
	"SetPortPriorities": func(ctx context.Context, c *Client) error { return c.SetPortPriorities(ctx, map[int]Priority{2: 1}) },
	"SetStormControl": func(ctx context.Context, c *Client) error {
		return c.SetStormControl(ctx, StormConfig{Port: 2, UnknownUnicast: true, Multicast: false, Broadcast: false, RateKbps: 64})
	},
	"SetHostname": func(ctx context.Context, c *Client) error { return c.SetHostname(ctx, "sw-test") },
	"SetVLAN8021Q": func(ctx context.Context, c *Client) error {
		return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 10, Name: "test", Untagged: []int{2}})
	},
	"DeleteVLAN8021Q": func(ctx context.Context, c *Client) error { return c.DeleteVLAN8021Q(ctx, 10) },
	"SetPVIDs":        func(ctx context.Context, c *Client) error { return c.SetPVIDs(ctx, map[int]int{2: 1}) },
	"SetVLANMode": func(ctx context.Context, c *Client) error {
		return c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 8})
	},
	"SetPortBasedVLAN": func(ctx context.Context, c *Client) error { return c.SetPortBasedVLAN(ctx, 2, []int{3, 4}) },
	"SetIPConfig": func(ctx context.Context, c *Client) error {
		// the address the fake switch already has, so an allowed write is confirmed at once
		return c.SetIPConfig(ctx, IPConfig{IP: net.IPv4(127, 0, 0, 1), Netmask: net.IPv4(255, 0, 0, 0), Gateway: net.IPv4(127, 0, 0, 2)})
	},
}

func writeMethodNames() []string {
	var names []string
	typ := reflect.TypeOf(&Client{})
	for i := 0; i < typ.NumMethod(); i++ {
		if n := typ.Method(i).Name; strings.HasPrefix(n, "Set") || strings.HasPrefix(n, "Delete") {
			names = append(names, n)
		}
	}
	return names
}

func TestEveryWriteMethodIsInTheGateTable(t *testing.T) {
	names := writeMethodNames()
	if len(names) < 17 { // a floor against a broken scan, not a count of the writers
		t.Fatalf("found only %d write methods by reflection; the scan is broken", len(names))
	}
	for _, n := range names {
		if writeCalls[n] == nil {
			t.Errorf("%s changes the switch but is not in writeCalls: add it, and check that it reaches the write gate", n)
		}
	}
	if len(writeCalls) != len(names) {
		t.Errorf("writeCalls has %d entries for %d write methods; remove the stale ones", len(writeCalls), len(names))
	}
}

// A gate that refuses must stop every write before a single SET is sent, and the
// method returns the gate's error unchanged.
func TestWriteGateRefusalSendsNothingForAnyWriteMethod(t *testing.T) {
	for _, name := range writeMethodNames() {
		call := writeCalls[name]
		if call == nil {
			continue // reported by TestEveryWriteMethodIsInTheGateTable
		}
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			var consulted int
			c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
				SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
				WriteGate: func(context.Context, InfoReader, Operation) error { consulted++; return errGateRefused }})
			c.allowLoopback = true
			t.Cleanup(func() { c.Close() })
			if err := call(context.Background(), c); !errors.Is(err, errGateRefused) {
				t.Fatalf("got %v, want the gate's error", err)
			}
			if consulted == 0 {
				t.Fatal("the gate was not consulted")
			}
			if sets := f.AllSets(); len(sets) != 0 {
				t.Fatalf("the switch received %d write(s) despite the refusal: %v", len(sets), sets)
			}
		})
	}
}

// With a gate that allows, the same calls reach the switch, so the test above
// proves the gate and not a broken argument.
func TestWriteGateThatAllowsLetsEveryWriteMethodThrough(t *testing.T) {
	for _, name := range writeMethodNames() {
		call := writeCalls[name]
		if call == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
				SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
				WriteGate: func(context.Context, InfoReader, Operation) error { return nil }})
			c.allowLoopback = true
			t.Cleanup(func() { c.Close() })
			if err := call(context.Background(), c); err != nil {
				t.Fatalf("an allowed write failed: %v", err)
			}
			if f.WriteCount() == 0 {
				t.Fatal("the write never reached the switch")
			}
		})
	}
}

// Reads are never gated: a gate that refuses everything must not stop a read.
func TestWriteGateDoesNotAffectReads(t *testing.T) {
	f := escptest.Start(t)
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(context.Context, InfoReader, Operation) error { return errGateRefused }})
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	if _, err := c.Info(ctx); err != nil {
		t.Fatalf("Info: %v", err)
	}
	if _, err := c.LED(ctx); err != nil {
		t.Fatalf("LED: %v", err)
	}
	if _, err := c.ConfigBackup(ctx); err != nil {
		t.Fatalf("ConfigBackup (an authenticated SET that only reads): %v", err)
	}
}

// The gate reads through the client it guards, so it must run outside the
// client's lock; this would deadlock otherwise.
func TestWriteGateMayReadThroughTheClient(t *testing.T) {
	f := escptest.Start(t)
	var firmware string
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(ctx context.Context, sw InfoReader, _ Operation) error {
			info, err := sw.Info(ctx)
			firmware = info.Firmware
			return err
		}})
	t.Cleanup(func() { c.Close() })
	if err := c.SetLED(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if firmware == "" {
		t.Fatal("the gate did not see the firmware version")
	}
}

// What the fake hands out is a copy, in its own type: changing it changes nothing in the fake,
// and the count agrees with the list.
func TestEscptestWriteRecordsAreCopies(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if f.WriteCount() != 0 || len(f.AllSets()) != 0 || len(f.LastSet()) != 0 {
		t.Fatal("a switch nobody wrote to has no writes")
	}
	if err := c.SetLED(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLED(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if f.WriteCount() != 2 || len(f.AllSets()) != 2 {
		t.Fatalf("two writes, got count %d and %d records", f.WriteCount(), len(f.AllSets()))
	}
	last := f.LastSet()
	if len(last) != 1 || last[0].Type != wire.TLVLED || string(last[0].Value) != "\x01" {
		t.Fatalf("the last write was the LED on: %+v", last)
	}
	last[0].Value[0] = 9
	f.AllSets()[0][0].Value[0] = 9
	if got := f.LastSet()[0].Value[0]; got != 1 {
		t.Errorf("changing the returned last write changed the fake: %d", got)
	}
	if got := f.AllSets()[0][0].Value[0]; got != 0 {
		t.Errorf("changing a returned write changed the fake: %d", got)
	}
}

// What every write method tells its gate: its own name, the ports of its arguments, and its class.
// Written out by hand, not derived from the implementation: the class is a decision (the worst the
// method can do), and changing one is a decision to review.
var writeOperations = map[string]Operation{
	"SetLED":            {Name: "SetLED", Impact: ImpactCosmetic},
	"SetHostname":       {Name: "SetHostname", Impact: ImpactCosmetic},
	"SetBandwidth":      {Name: "SetBandwidth", Ports: []int{2}, Impact: ImpactTraffic},
	"SetIGMP":           {Name: "SetIGMP", Impact: ImpactTraffic},
	"SetPort":           {Name: "SetPort", Ports: []int{2}, Impact: ImpactTraffic},
	"SetPortMirror":     {Name: "SetPortMirror", Ports: []int{2, 3}, Impact: ImpactTraffic},
	"SetQoSMode":        {Name: "SetQoSMode", Impact: ImpactTraffic},
	"SetPortPriorities": {Name: "SetPortPriorities", Ports: []int{2}, Impact: ImpactTraffic},
	"SetStormControl":   {Name: "SetStormControl", Ports: []int{2}, Impact: ImpactTraffic},
	"SetVLAN8021Q":      {Name: "SetVLAN8021Q", Ports: []int{2}, Impact: ImpactTraffic, Replaces: GroupDot1QVLAN, ID: 10},
	"DeleteVLAN8021Q":   {Name: "DeleteVLAN8021Q", Impact: ImpactTraffic, Replaces: GroupDot1QVLAN, ID: 10},
	"SetPortBasedVLAN":  {Name: "SetPortBasedVLAN", Ports: []int{3, 4}, Impact: ImpactTraffic, Replaces: GroupPortVLAN, ID: 2},
	"SetLAG":            {Name: "SetLAG", Ports: []int{2, 3}, Impact: ImpactManagement, Replaces: GroupLAG, ID: 1},
	"SetLoopPrevention": {Name: "SetLoopPrevention", Impact: ImpactManagement},
	"SetPVIDs":          {Name: "SetPVIDs", Ports: []int{2}, Impact: ImpactManagement},
	"SetVLANMode":       {Name: "SetVLANMode", Ports: []int{8}, Impact: ImpactManagement},
	"SetIPConfig":       {Name: "SetIPConfig", Impact: ImpactManagement},
}

// operationsOf runs call against a client whose gate allows and records what the gate was told.
func operationsOf(t *testing.T, call func(context.Context, *Client) error) []Operation {
	t.Helper()
	f := escptest.Start(t)
	var seen []Operation
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(_ context.Context, _ InfoReader, op Operation) error { seen = append(seen, op); return nil }})
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	if err := call(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	return seen
}

func TestEveryWriteMethodDeclaresItsOperation(t *testing.T) {
	names := writeMethodNames()
	if len(writeOperations) != len(names) {
		t.Fatalf("writeOperations has %d entries for %d write methods", len(writeOperations), len(names))
	}
	for _, name := range names {
		want, ok := writeOperations[name]
		if !ok {
			t.Errorf("%s is not in writeOperations: decide its class and the ports it names", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			seen := operationsOf(t, writeCalls[name])
			if len(seen) == 0 {
				t.Fatal("the gate was not consulted")
			}
			for i, op := range seen {
				if !reflect.DeepEqual(op, want) {
					t.Errorf("consultation %d: gate was told %+v, want %+v", i+1, op, want)
				}
			}
		})
	}
}

// The ports an operation names follow the arguments, not a fixed shape.
func TestOperationPortsFollowTheArguments(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name  string
		call  func(c *Client) error
		ports []int
	}{
		{"a mirror names its destination and both kinds of source", func(c *Client) error {
			return c.SetPortMirror(ctx, Mirror{Enabled: true, MirrorPort: 5, IngressPorts: []int{1, 2}, EgressPorts: []int{3}})
		}, []int{5, 1, 2, 3}},
		{"a disabled mirror names none", func(c *Client) error { return c.SetPortMirror(ctx, Mirror{}) }, nil},
		{"an 802.1Q VLAN names its tagged ports and then its untagged ones", func(c *Client) error {
			return c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20, Tagged: []int{8}, Untagged: []int{3, 4}})
		}, []int{8, 3, 4}},
		{"several PVIDs are named in port order", func(c *Client) error { return c.SetPVIDs(ctx, map[int]int{6: 1, 2: 1, 4: 1}) }, []int{2, 4, 6}},
		{"several priorities are named in port order", func(c *Client) error {
			return c.SetPortPriorities(ctx, map[int]Priority{7: 1, 3: 2})
		}, []int{3, 7}},
		{"the MTU VLAN names its uplink", func(c *Client) error {
			return c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 5})
		}, []int{5}},
		{"the other VLAN modes name no port", func(c *Client) error {
			return c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModeDot1Q})
		}, nil},
		{"a LAG that is removed names none", func(c *Client) error { return c.SetLAG(ctx, 2, nil) }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := operationsOf(t, func(_ context.Context, c *Client) error { return tc.call(c) })
			if len(seen) == 0 {
				t.Fatal("the gate was not consulted")
			}
			if !reflect.DeepEqual(seen[0].Ports, tc.ports) && (len(seen[0].Ports) != 0 || len(tc.ports) != 0) {
				t.Fatalf("ports %v, want %v", seen[0].Ports, tc.ports)
			}
		})
	}
}

// A gate that changes the ports it was given changes nothing that is sent.
func TestGateGetsItsOwnCopyOfThePorts(t *testing.T) {
	f := escptest.Start(t)
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 300 * time.Millisecond,
		SwitchPort: f.Port(), BroadcastAddr: "127.0.0.1",
		WriteGate: func(_ context.Context, _ InfoReader, op Operation) error {
			for i := range op.Ports {
				op.Ports[i] = 8
			}
			return nil
		}})
	t.Cleanup(func() { c.Close() })
	ports := []int{3, 4}
	if err := c.SetPortBasedVLAN(context.Background(), 2, ports); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ports, []int{3, 4}) {
		t.Fatalf("the caller's slice was changed through the gate: %v", ports)
	}
	if got := f.LastSet(); len(got) != 1 || !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0x0c}) {
		t.Fatalf("the write must carry ports 3 and 4, got %+v", got)
	}
}

func TestImpactNames(t *testing.T) {
	for impact, want := range map[Impact]string{ImpactCosmetic: "cosmetic", ImpactTraffic: "traffic", ImpactManagement: "management", 0: "unknown(0)", 7: "unknown(7)"} {
		if got := impact.String(); got != want {
			t.Errorf("Impact(%d) = %q, want %q", int(impact), got, want)
		}
	}
	if ImpactCosmetic >= ImpactTraffic || ImpactTraffic >= ImpactManagement {
		t.Fatal("the classes are ordered from harmless to able to cut the management path; gates compare them")
	}
}

// Removing a group names no port, but the operation still says which group goes: a gate cannot
// protect the members without it.
func TestARemovedGroupIsStillReportedAsReplaced(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		call func(c *Client) error
		want Operation
	}{
		{"LAG", func(c *Client) error { return c.SetLAG(ctx, 2, nil) }, Operation{Name: "SetLAG", Impact: ImpactManagement, Replaces: GroupLAG, ID: 2}},
		{"port-based VLAN", func(c *Client) error { return c.SetPortBasedVLAN(ctx, 3, nil) }, Operation{Name: "SetPortBasedVLAN", Impact: ImpactTraffic, Replaces: GroupPortVLAN, ID: 3}},
		{"802.1Q VLAN", func(c *Client) error { return c.DeleteVLAN8021Q(ctx, 20) }, Operation{Name: "DeleteVLAN8021Q", Impact: ImpactTraffic, Replaces: GroupDot1QVLAN, ID: 20}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen := operationsOf(t, func(_ context.Context, c *Client) error { return tc.call(c) })
			if len(seen) == 0 {
				t.Fatal("the gate was not consulted")
			}
			got := seen[0]
			if len(got.Ports) != 0 {
				t.Errorf("a removal names no port, got %v", got.Ports)
			}
			got.Ports = nil
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Every place that sends an OpSet is known. Two are not writes through setTLVs (the session key
// exchange and the configuration backup, which only reads), and are the only ones allowed; a new
// one would have to be gated, or be a read on purpose, and this test makes that a decision.
func TestOnlyKnownPlacesSendASET(t *testing.T) {
	want := map[string]int{
		"session.go": 1, // the key exchange in ensureSession
		"request.go": 1, // setTLVsSent, behind the write gate
		"backup.go":  1, // ConfigBackup: a SET on the wire, a read in effect
	}
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("cannot list the package sources: %v", err)
	}
	pattern := regexp.MustCompile(`\bwire\.OpSet\b`)
	got := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(pattern.FindAll(src, -1)); n > 0 {
			got[f] = n
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("places that send an OpSet: %v, expected %v. A new SET must pass the write gate (setTLVs) or be a read by design.", got, want)
	}
}
