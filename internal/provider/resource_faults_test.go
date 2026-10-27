package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

func loopCfg(v string) string {
	return providerBlock + "resource \"tplinkeasysmart_loop_prevention\" \"this\" {\n  enabled = " + v + "\n}\n"
}

func qosCfg(body string) string {
	return providerBlock + "resource \"tplinkeasysmart_qos\" \"this\" {\n" + body + "}\n"
}

// faultScenario describes how one resource must behave when the switch misbehaves.
// Every resource is driven through the same failure modes, so a regression in
// the shared pattern (write, read back, verify, refresh, import) shows up
// everywhere, and each message is asserted so a swallowed error cannot pass.
type faultScenario struct {
	name string
	addr string
	// fixture: the switch state and two different valid configurations. create
	// must differ from the fake's initial state so an ignored write is visible.
	fake           func() *fakeClient
	create, update string
	// the client method that performs the write, and the error the resource reports for it
	writeMethod, writeErr string
	// the call that reads the written value back (nth call of the method) and the error reported
	backMethod       string
	backCall         int
	backErr, noChgEr string
	// the call used at refresh time and the error reported for it
	readMethod, readErr string
	// a malformed import id and the error reported for it
	importBad, importErr string
	// makes the object disappear from the switch (nil: the resource is a singleton)
	vanish func(f *fakeClient)
}

func faultScenarios() []faultScenario {
	return []faultScenario{
		{
			name: "led", addr: "tplinkeasysmart_led.this",
			fake:   func() *fakeClient { return &fakeClient{led: true} },
			create: ledConfig("false"), update: ledConfig("true"),
			writeMethod: "SetLED", writeErr: "Setting LED failed",
			backMethod: "LED", backCall: 1, backErr: "Reading LED back failed", noChgEr: "LED state did not change",
			readMethod: "LED", readErr: "Reading LED failed",
			importBad: "nope", importErr: "only valid id",
		},
		{
			name: "igmp snooping", addr: "tplinkeasysmart_igmp_snooping.this",
			fake:   func() *fakeClient { return &fakeClient{igmp: escp.IGMP{Enabled: true}} },
			create: igmpConfig("false"), update: igmpConfig("true"),
			writeMethod: "SetIGMP", writeErr: "Setting IGMP snooping failed",
			backMethod: "IGMP", backCall: 1, backErr: "Reading IGMP snooping back failed", noChgEr: "IGMP snooping did not change",
			readMethod: "IGMP", readErr: "Reading IGMP snooping failed",
			importBad: "nope", importErr: "only valid id",
		},
		{
			name: "loop prevention", addr: "tplinkeasysmart_loop_prevention.this",
			fake:   func() *fakeClient { return &fakeClient{loop: true} },
			create: loopCfg("false"), update: loopCfg("true"),
			writeMethod: "SetLoopPrevention", writeErr: "Setting loop prevention failed",
			backMethod: "LoopPrevention", backCall: 1, backErr: "Reading loop prevention back failed", noChgEr: "Loop prevention did not change",
			readMethod: "LoopPrevention", readErr: "Reading loop prevention failed",
			importBad: "nope", importErr: "only valid id",
		},
		{
			name: "port", addr: "tplinkeasysmart_port.p",
			fake:   func() *fakeClient { return &fakeClient{ports: testPorts()} },
			create: portConfig(7, "  flow_control = true\n"), update: portConfig(7, "  speed = \"100Mbps-full\"\n"),
			writeMethod: "SetPort", writeErr: "Setting port failed",
			backMethod: "Ports", backCall: 2, backErr: "Reading port back failed", noChgEr: "Port did not change",
			readMethod: "Ports", readErr: "Reading port failed",
			importBad: "abc", importErr: "expected a port number",
			vanish: func(f *fakeClient) { f.ports = f.ports[:2] },
		},
		{
			name: "qos", addr: "tplinkeasysmart_qos.this",
			fake:   newFakeQoS,
			create: qosCfg("  mode = \"802.1p\"\n"), update: qosCfg("  mode = \"dscp-802.1p\"\n"),
			writeMethod: "SetQoSMode", writeErr: "Setting QoS mode failed",
			backMethod: "QoS", backCall: 1, backErr: "Reading QoS back failed", noChgEr: "QoS mode did not change",
			readMethod: "QoS", readErr: "Reading QoS failed",
			importBad: "nope", importErr: "only valid id",
		},
		{
			name: "bandwidth limit", addr: "tplinkeasysmart_bandwidth_limit.b",
			fake:   func() *fakeClient { return &fakeClient{ports: testPorts(), bw: map[escp.PortID][2]int{}} },
			create: bwConfig(7, "  ingress_kbps = 64\n"), update: bwConfig(7, "  egress_kbps = 64\n"),
			writeMethod: "SetBandwidth", writeErr: "Setting bandwidth limit failed",
			backMethod: "Bandwidth", backCall: 1, backErr: "Bandwidth limit did not change", noChgEr: "Bandwidth limit did not change",
			readMethod: "Bandwidth", readErr: "Reading bandwidth limit failed",
			importBad: "abc", importErr: "expected a port number",
			vanish: func(f *fakeClient) { f.rowPorts = []int{1, 4} },
		},
		{
			name: "storm limit", addr: "tplinkeasysmart_storm_limit.s",
			fake:   func() *fakeClient { return &fakeClient{ports: testPorts(), storm: map[escp.PortID]escp.PortStorm{}} },
			create: stormConfig(7, "  broadcast = true\n  rate_kbps = 64\n"), update: stormConfig(7, "  multicast = true\n  rate_kbps = 128\n"),
			writeMethod: "SetStormControl", writeErr: "Setting storm control failed",
			backMethod: "StormControl", backCall: 1, backErr: "Storm control did not change", noChgEr: "Storm control did not change",
			readMethod: "StormControl", readErr: "Reading storm control failed",
			importBad: "abc", importErr: "expected a port number",
			vanish: func(f *fakeClient) { f.rowPorts = []int{1, 4} },
		},
		{
			name: "port mirror", addr: "tplinkeasysmart_port_mirror.m",
			fake:        func() *fakeClient { return &fakeClient{ports: testPorts(), mirror: &escp.Mirror{}} },
			create:      mirrorConfig("  enabled = true\n  mirror_port = 7\n  ingress_ports = [4]\n"),
			update:      mirrorConfig("  enabled = true\n  mirror_port = 7\n  ingress_ports = [1, 4]\n"),
			writeMethod: "SetPortMirror", writeErr: "Setting port mirror failed",
			backMethod: "PortMirror", backCall: 1, backErr: "Reading port mirror back failed", noChgEr: "Port mirror did not change",
			readMethod: "PortMirror", readErr: "Reading port mirror failed",
			importBad: "nope", importErr: "only valid id",
		},
		{
			name: "lag", addr: "tplinkeasysmart_lag.l",
			fake: func() *fakeClient {
				return &fakeClient{ports: lagPorts(), lags: map[escp.LAGID]escp.PortSet{1: escp.MustPortSet(1, 2)}}
			},
			create: lagConfig(2, "  ports = [6, 7]\n"), update: lagConfig(2, "  ports = [5, 6, 7]\n"),
			writeMethod: "SetLAG", writeErr: "Setting LAG failed",
			backMethod: "LAGs", backCall: 2, backErr: "LAG did not change", noChgEr: "LAG did not change",
			readMethod: "LAGs", readErr: "Reading LAG failed",
			importBad: "abc", importErr: "expected a LAG group number",
			vanish: func(f *fakeClient) { f.lags[2] = 0 },
		},
		{
			name: "port-based vlan group", addr: "tplinkeasysmart_vlan_port_based_group.v",
			fake: func() *fakeClient {
				return &fakeClient{ports: lagPorts(), pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)}}
			},
			create: vlanGroupConfig(2, "  ports = [6, 7]\n"), update: vlanGroupConfig(2, "  ports = [6]\n"),
			writeMethod: "SetPortBasedVLAN", writeErr: "Setting VLAN group failed",
			backMethod: "PortBasedVLAN", backCall: 2, backErr: "VLAN group did not change", noChgEr: "VLAN group did not change",
			readMethod: "PortBasedVLAN", readErr: "Reading VLAN group failed",
			importBad: "abc", importErr: "expected a VLAN id",
			vanish: func(f *fakeClient) { delete(f.pvlans, 2) },
		},
	}
}

func TestResourcesReportASwitchThatRejectsTheWrite(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			inner := sc.fake()
			fc := newFaulty(inner)
			fc.failAlways(sc.writeMethod)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps:                    []resource.TestStep{{Config: sc.create, ExpectError: regexp.MustCompile(sc.writeErr)}},
			})
			if inner.sets != 0 {
				t.Fatalf("a rejected write must not reach the fake switch, %d write(s) did", inner.sets)
			}
		})
	}
}

func TestResourcesReportAFailingReadBackAfterTheWrite(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(sc.fake())
			fc.failCalls(sc.backMethod, sc.backCall, sc.backCall)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps:                    []resource.TestStep{{Config: sc.create, ExpectError: regexp.MustCompile(sc.backErr)}},
			})
		})
	}
}

func TestResourcesDetectADeviceThatAcceptsButIgnoresTheWrite(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(sc.fake())
			fc.setIgnoreWrites(true)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps:                    []resource.TestStep{{Config: sc.create, ExpectError: regexp.MustCompile(sc.noChgEr)}},
			})
		})
	}
}

func TestResourcesKeepTheirStateWhenAnUpdateFails(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(sc.fake())
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps: []resource.TestStep{
					{Config: sc.create},
					{PreConfig: func() { fc.failAlways(sc.writeMethod) }, Config: sc.update, ExpectError: regexp.MustCompile(sc.writeErr)},
					// the failed update must not have been recorded: the original configuration is still clean
					{PreConfig: fc.heal, Config: sc.create, PlanOnly: true},
				},
			})
		})
	}
}

func TestResourcesReportAFailingRefresh(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(sc.fake())
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps: []resource.TestStep{
					{Config: sc.create},
					{PreConfig: func() { fc.failAlways(sc.readMethod) }, Config: sc.create, ExpectError: regexp.MustCompile(sc.readErr)},
					{PreConfig: fc.heal, Config: sc.create, PlanOnly: true}, // lets the final destroy succeed
				},
			})
		})
	}
}

func TestResourcesRejectMalformedImportIDs(t *testing.T) {
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(sc.fake())
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps: []resource.TestStep{
					{Config: sc.create},
					{ResourceName: sc.addr, ImportState: true, ImportStateId: sc.importBad, ExpectError: regexp.MustCompile(sc.importErr)},
				},
			})
		})
	}
}

func TestKeyedResourcesAreRecreatedWhenTheObjectVanishesFromTheSwitch(t *testing.T) {
	for _, sc := range faultScenarios() {
		if sc.vanish == nil {
			continue
		}
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			inner := sc.fake()
			fc := newFaulty(inner)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps: []resource.TestStep{
					{Config: sc.create},
					{PreConfig: func() { inner.mutate(sc.vanish) }, Config: sc.create, PlanOnly: true, ExpectNonEmptyPlan: true},
				},
			})
		})
	}
}

// guardCases are write attempts on a port that carries a link; the switch must
// never see a write.
func TestGuardedResourcesRefuseUsedPortsWithoutWriting(t *testing.T) {
	cases := []struct {
		name, cfg, want string
		fake            func() *fakeClient
	}{
		{"port on a linked port", portConfig(4, "  enabled = false\n"), `has a link up`, func() *fakeClient { return &fakeClient{ports: testPorts()} }},
		{"bandwidth on a LAG member with a link", bwConfig(1, "  ingress_kbps = 64\n"), `port 1 has a link up`, func() *fakeClient { return &fakeClient{ports: testPorts(), bw: map[escp.PortID][2]int{}} }},
		{"storm on a linked port", stormConfig(4, "  broadcast = true\n  rate_kbps = 64\n"), `has a link up`, func() *fakeClient { return &fakeClient{ports: testPorts(), storm: map[escp.PortID]escp.PortStorm{}} }},
		{"mirror destination on a linked port", mirrorConfig("  enabled = true\n  mirror_port = 4\n  ingress_ports = [7]\n"), `has a link up`, func() *fakeClient { return &fakeClient{ports: testPorts(), mirror: &escp.Mirror{}} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := tc.fake()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(inner),
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if inner.sets != 0 {
				t.Fatalf("the guard must prevent every write, %d happened", inner.sets)
			}
		})
	}
}

func TestGuardCannotBeBypassedByAFailingPortLookup(t *testing.T) {
	// If the switch cannot tell which ports are in use, the guard must fail
	// closed: no write, and an error saying why.
	cases := []struct{ name, cfg string }{
		{"port", portConfig(7, "  flow_control = true\n")},
		{"bandwidth", bwConfig(7, "  ingress_kbps = 64\n")},
		{"storm", stormConfig(7, "  broadcast = true\n  rate_kbps = 64\n")},
		{"mirror", mirrorConfig("  enabled = true\n  mirror_port = 7\n  ingress_ports = [4]\n")},
		{"lag", lagConfig(2, "  ports = [6, 7]\n")},
		{"vlan group", vlanGroupConfig(2, "  ports = [6, 7]\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &fakeClient{ports: lagPorts(), lags: map[escp.LAGID]escp.PortSet{1: escp.MustPortSet(1, 2)}, pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)},
				bw: map[escp.PortID][2]int{}, storm: map[escp.PortID]escp.PortStorm{}, mirror: &escp.Mirror{}}
			// testPorts has port 7; lagPorts too
			fc := newFaulty(inner)
			fc.failAlways("Ports")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(`cannot check whether the`)}},
			})
			if inner.sets != 0 {
				t.Fatalf("a failing port lookup must not allow a write, %d happened", inner.sets)
			}
		})
	}
}

func TestGuardRejectsPortsThatDoNotExist(t *testing.T) {
	cases := []struct{ name, cfg string }{
		{"port", portConfig(9, "  flow_control = true\n")},
		{"bandwidth", bwConfig(9, "  ingress_kbps = 64\n")},
		{"storm", stormConfig(9, "  broadcast = true\n  rate_kbps = 64\n")},
		{"mirror", mirrorConfig("  enabled = true\n  mirror_port = 9\n  ingress_ports = [4]\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := &fakeClient{ports: testPorts(), bw: map[escp.PortID][2]int{}, storm: map[escp.PortID]escp.PortStorm{}, mirror: &escp.Mirror{}}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(inner),
				Steps:                    []resource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(`port 9 does not exist`)}},
			})
			if inner.sets != 0 {
				t.Fatalf("%d write(s) happened", inner.sets)
			}
		})
	}
}

func TestAllowDisruptiveReallyAllowsTheWrite(t *testing.T) {
	cases := []struct {
		name, cfg string
		fake      func() *fakeClient
		applied   func(f *fakeClient) bool
	}{
		{"port on a linked port", portConfig(4, "  flow_control = true\n  allow_disruptive = true\n"),
			func() *fakeClient { return &fakeClient{ports: testPorts()} },
			func(f *fakeClient) bool { return f.ports[1].ConfiguredFlowControl }},
		{"bandwidth on a linked port", bwConfig(4, "  ingress_kbps = 64\n  allow_disruptive = true\n"),
			func() *fakeClient { return &fakeClient{ports: testPorts(), bw: map[escp.PortID][2]int{}} },
			func(f *fakeClient) bool { return f.bw[4][0] == 64 }},
		{"storm on a linked port", stormConfig(4, "  broadcast = true\n  rate_kbps = 64\n  allow_disruptive = true\n"),
			func() *fakeClient { return &fakeClient{ports: testPorts(), storm: map[escp.PortID]escp.PortStorm{}} },
			func(f *fakeClient) bool { return f.storm[4].RateKbps == 64 }},
		{"mirror to a linked port", mirrorConfig("  enabled = true\n  mirror_port = 4\n  ingress_ports = [7]\n  allow_disruptive = true\n"),
			func() *fakeClient { return &fakeClient{ports: testPorts(), mirror: &escp.Mirror{}} },
			func(f *fakeClient) bool { return f.mirror.Enabled && f.mirror.MirrorPort == 4 }},
		{"LAG over a linked port", lagConfig(1, "  ports = [3, 4]\n  allow_disruptive = true\n"),
			func() *fakeClient {
				return &fakeClient{ports: lagPorts(), lags: map[escp.LAGID]escp.PortSet{1: escp.MustPortSet(1, 2)}}
			},
			func(f *fakeClient) bool { return f.lags[1] == escp.MustPortSet(3, 4) }},
		{"VLAN group over a linked port", vlanGroupConfig(2, "  ports = [4]\n  allow_disruptive = true\n"),
			func() *fakeClient {
				return &fakeClient{ports: lagPorts(), pvlans: map[escp.PortVLANID]escp.PortSet{1: escp.MustPortSet(1, 2, 4, 6, 7)}}
			},
			func(f *fakeClient) bool { return f.pvlans[2] == escp.MustPortSet(4) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := tc.fake()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(inner),
				Steps: []resource.TestStep{{Config: tc.cfg, Check: func(*terraformState) error {
					inner.mu.Lock()
					defer inner.mu.Unlock()
					if !tc.applied(inner) {
						return fmt.Errorf("the explicit override did not result in a write")
					}
					return nil
				}}},
			})
		})
	}
}
