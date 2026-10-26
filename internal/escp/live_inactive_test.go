//go:build live

package escp

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Measures what the switch does with values of a feature that is not active: whether a write is
// accepted, whether it reads back, whether it survives a switch of the VLAN or QoS mode. The
// answers decide, per setting, whether the provider may let a configuration stage such a value
// (docs/inactive-settings-design.md in the private workspace). Nothing is asserted about the
// answers: every observation is logged as a line starting with "OBS|" for the run record. What is
// asserted is the restore: the switch must end exactly as it began.
//
// Only unused test ports are written (TPLINK_EASYSMART_LIVE_PORTS="a,b", proven link-down and in
// no LAG), the uplink and VLAN 1 are never changed, and the MTU VLAN is deliberately not probed
// (a misread write could enable it without its uplink and cut the management path). It switches
// the VLAN mode several times, so it needs TPLINK_EASYSMART_LIVE_INACTIVE=1 and a switch that
// starts in port-based mode. Take a configuration backup first and watch the pings.
func TestLiveInactiveSettings(t *testing.T) {
	if os.Getenv("TPLINK_EASYSMART_LIVE_INACTIVE") != "1" {
		t.Skip("set TPLINK_EASYSMART_LIVE_INACTIVE=1 to probe inactive settings (switches the VLAN mode several times)")
	}
	a, b, _ := liveTestPorts(t)
	c, ctx := liveClient(t), context.Background()
	livePort(t, c, a)
	livePort(t, c, b)

	obs := func(format string, args ...any) { t.Logf("OBS|"+format, args...) }
	// result describes a write's outcome: ok, or the switch's refusal code.
	result := func(err error) string {
		var rej *RejectedError
		switch {
		case err == nil:
			return "accepted"
		case errors.As(err, &rej):
			return fmt.Sprintf("REFUSED code %d", rej.Code)
		default:
			return "ERROR " + err.Error()
		}
	}
	// raw prints what the switch really answers, before the parsers decide what to show.
	raw := func(types ...int16) string {
		tlvs, err := c.getTLVs(ctx, types...)
		if err != nil {
			return "read error: " + err.Error()
		}
		var parts []string
		for _, tlv := range tlvs {
			parts = append(parts, fmt.Sprintf("%d:%s", tlv.Type, hex.EncodeToString(tlv.Value)))
		}
		return strings.Join(parts, " ")
	}
	dot1q := func() string {
		q, err := c.Dot1Q(ctx)
		if err != nil {
			return "Dot1Q read error: " + err.Error()
		}
		return fmt.Sprintf("enabled=%v vlans=%+v pvids=%+v", q.Enabled, q.VLANs, q.PVIDs)
	}
	portBased := func() string {
		v, err := c.PortBasedVLAN(ctx)
		if err != nil {
			return "PortBasedVLAN read error: " + err.Error()
		}
		return fmt.Sprintf("enabled=%v max=%d groups=%+v", v.Enabled, v.MaxVLAN, v.VLANs)
	}
	snapshot := func() string {
		var s []string
		s = append(s, "port-based: "+portBased(), "802.1Q: "+dot1q())
		mtu, err := c.MTUVLAN(ctx)
		s = append(s, fmt.Sprintf("mtu: %+v %v", mtu, err))
		qos, err := c.QoS(ctx)
		s = append(s, fmt.Sprintf("qos: %+v %v", qos, err))
		m, err := c.PortMirror(ctx)
		s = append(s, fmt.Sprintf("mirror: %+v %v", m, err))
		g, err := c.IGMP(ctx)
		s = append(s, fmt.Sprintf("igmp: enabled=%v suppression=%v %v", g.Enabled, g.ReportSuppression, err))
		return strings.Join(s, "\n")
	}

	mtu0, pb0, q0 := vlanModes(t, c)
	if mtu0.Enabled || !pb0.Enabled || q0.Enabled {
		t.Fatalf("refusing to start: want port-based mode only, have mtu=%+v port-based=%v 802.1Q=%v", mtu0, pb0.Enabled, q0.Enabled)
	}
	start := snapshot()
	obs("start state:\n%s", start)
	qos0, err := c.QoS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	igmp0, err := c.IGMP(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The restore runs whatever happened: VLAN leftovers out of the way, port-based mode back,
	// QoS, mirror and IGMP as they were, then the comparison with the start state.
	defer func() {
		if q, err := c.Dot1Q(ctx); err == nil && q.Enabled {
			if err := c.SetPVIDs(ctx, map[int]int{a: 1, b: 1}); err != nil {
				t.Errorf("RESTORE: PVIDs: %v", err)
			}
			if err := c.DeleteVLAN8021Q(ctx, 20); err != nil {
				t.Errorf("RESTORE: delete VLAN 20: %v", err)
			}
		}
		if err := c.SetVLANMode(ctx, VLANModeSetting{Mode: VLANModePortBased}); err != nil {
			t.Errorf("RESTORE FAILED: port-based mode: %v", err)
		}
		if err := c.SetPortBasedVLAN(ctx, 2, nil); err != nil {
			t.Logf("RESTORE: removing group 2: %v", err)
		}
		if err := c.SetQoSMode(ctx, QoSPortBased); err != nil {
			t.Errorf("RESTORE: QoS mode to port-based for the priorities: %v", err)
		}
		var back []PortPriority
		for _, p := range qos0.Priorities {
			if p.Port == a || p.Port == b {
				back = append(back, p)
			}
		}
		changes := map[int]Priority{}
		for _, p := range back {
			changes[p.Port] = p.Priority
		}
		if len(changes) > 0 {
			if err := c.SetPortPriorities(ctx, changes); err != nil {
				t.Errorf("RESTORE: priorities: %v", err)
			}
		}
		if err := c.SetQoSMode(ctx, qos0.Mode); err != nil {
			t.Errorf("RESTORE: QoS mode: %v", err)
		}
		if err := c.SetPortMirror(ctx, Mirror{}); err != nil {
			t.Errorf("RESTORE: mirror: %v", err)
		}
		if err := c.SetIGMP(ctx, IGMPConfig{Enabled: igmp0.Enabled, ReportSuppression: igmp0.ReportSuppression}); err != nil {
			t.Errorf("RESTORE: IGMP: %v", err)
		}
		if end := snapshot(); end != start {
			t.Errorf("NOT RESTORED:\n--- start\n%s\n--- end\n%s", start, end)
		} else {
			obs("restored: the final state equals the start state")
		}
	}()

	setMode := func(m VLANMode) {
		t.Helper()
		if err := c.SetVLANMode(ctx, VLANModeSetting{Mode: m}); err != nil {
			t.Fatalf("switching to %s: %v", m, err)
		}
	}

	// A. 802.1Q VLAN and PVID staged while the switch is in port-based mode.
	obs("A0 port-based mode, raw 802.1Q reads: %s", raw(wire.TLVVLAN8021Q, wire.TLVVLAN8021QVLAN, wire.TLVVLAN8021QPVID))
	err = c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20, Name: "staged", Untagged: []int{a}, Tagged: []int{b}})
	obs("A1 SetVLAN8021Q(20) while 802.1Q is off: %s", result(err))
	obs("A1 read right after: %s | raw %s", dot1q(), raw(wire.TLVVLAN8021Q, wire.TLVVLAN8021QVLAN, wire.TLVVLAN8021QPVID))
	err = c.SetPVIDs(ctx, map[int]int{a: 20})
	obs("A2 SetPVIDs(a=20) while 802.1Q is off: %s", result(err))
	obs("A2 read right after: %s", dot1q())
	setMode(VLANModeDot1Q)
	obs("A3 after switching 802.1Q on: %s", dot1q())
	obs("A3 port-based view while 802.1Q is on: %s | raw %s", portBased(), raw(wire.TLVVLANPortBased, wire.TLVVLANPortBasedVID))
	// leave 802.1Q clean before the next probe
	if q, err := c.Dot1Q(ctx); err == nil {
		if _, ok := vlanByID(q, 20); ok {
			_ = c.SetPVIDs(ctx, map[int]int{a: 1, b: 1})
			if err := c.DeleteVLAN8021Q(ctx, 20); err != nil {
				t.Fatalf("cleaning VLAN 20: %v", err)
			}
		}
	}

	// B. A port-based group staged while the switch is in 802.1Q mode.
	err = c.SetPortBasedVLAN(ctx, 2, []int{a, b})
	obs("B1 SetPortBasedVLAN(2,[a b]) while port-based is off: %s", result(err))
	obs("B1 read right after: %s | raw %s", portBased(), raw(wire.TLVVLANPortBased, wire.TLVVLANPortBasedVID))
	setMode(VLANModePortBased)
	obs("B2 after switching port-based on: %s", portBased())
	_ = c.SetPortBasedVLAN(ctx, 2, nil)
	obs("B2 after removing group 2: %s", portBased())

	// C. Does a table survive a trip through the other mode?
	if err := c.SetPortBasedVLAN(ctx, 2, []int{a, b}); err != nil {
		t.Fatalf("creating the test group in port-based mode: %v", err)
	}
	obs("C1 group 2 created in port-based mode: %s", portBased())
	setMode(VLANModeDot1Q)
	obs("C1 in 802.1Q mode: %s", dot1q())
	setMode(VLANModePortBased)
	obs("C1 back in port-based mode (trip through 802.1Q): %s", portBased())
	_ = c.SetPortBasedVLAN(ctx, 2, nil)
	setMode(VLANModeDot1Q)
	if err := c.SetVLAN8021Q(ctx, VLAN8021Q{ID: 20, Name: "trip", Untagged: []int{a}, Tagged: []int{b}}); err != nil {
		t.Fatalf("creating the test VLAN in 802.1Q mode: %v", err)
	}
	obs("C2 VLAN 20 created in 802.1Q mode: %s", dot1q())
	setMode(VLANModePortBased)
	obs("C2 in port-based mode: %s | raw 802.1Q %s", portBased(), raw(wire.TLVVLAN8021Q, wire.TLVVLAN8021QVLAN))
	setMode(VLANModeDot1Q)
	obs("C2 back in 802.1Q mode (trip through port-based): %s", dot1q())
	_ = c.SetPVIDs(ctx, map[int]int{a: 1, b: 1})
	_ = c.DeleteVLAN8021Q(ctx, 20)
	setMode(VLANModePortBased)

	// D. QoS priorities in a mode where they are ignored.
	if qos0.Mode == QoSPortBased {
		if err := c.SetQoSMode(ctx, QoSDSCP8021p); err != nil {
			t.Fatalf("QoS mode: %v", err)
		}
	}
	var prio0 Priority
	for _, p := range qos0.Priorities {
		if p.Port == a {
			prio0 = p.Priority
		}
	}
	staged := PriorityHighest
	if prio0 == staged {
		staged = PriorityLowest
	}
	err = c.SetPortPriorities(ctx, map[int]Priority{a: staged})
	obs("D1 SetPortPriorities(a=%d) in a QoS mode where priorities are ignored: %s", staged, result(err))
	q, _ := c.QoS(ctx)
	obs("D1 read right after: mode=%s priority of a=%v | raw %s", q.Mode, prioOf(q, a), raw(wire.TLVQoSMode, wire.TLVQoSPriority))
	if err := c.SetQoSMode(ctx, QoSPortBased); err != nil {
		t.Fatalf("QoS mode to port-based: %v", err)
	}
	q, _ = c.QoS(ctx)
	obs("D2 after switching to port-based: mode=%s priority of a=%v (staged %d, original %d)", q.Mode, prioOf(q, a), staged, prio0)

	// E. A disabled mirror entry that carries ports. The parser zeroes a disabled entry, so the raw bytes matter.
	ingress, err := maskFromPorts([]int{a})
	if err != nil {
		t.Fatal(err)
	}
	entry := append([]byte{0, byte(b)}, append(ingress, make([]byte, maskLen)...)...)
	err = c.setTLVs(ctx, Operation{Name: "probe: disabled mirror with ports", Impact: ImpactTraffic}, wire.TLV{Type: wire.TLVMirror, Value: entry})
	obs("E1 mirror entry enabled=0 dest=b ingress=a: %s | raw read %s", result(err), raw(wire.TLVMirror))

	// F. IGMP report suppression with snooping off.
	err = c.SetIGMP(ctx, IGMPConfig{Enabled: false, ReportSuppression: true})
	g, _ := c.IGMP(ctx)
	obs("F1 SetIGMP(enabled=false, suppression=true): %s | read enabled=%v suppression=%v | raw %s", result(err), g.Enabled, g.ReportSuppression, raw(wire.TLVIGMPEnabled, wire.TLVIGMPSuppression))
	err = c.SetIGMP(ctx, IGMPConfig{Enabled: true, ReportSuppression: g.ReportSuppression})
	g, _ = c.IGMP(ctx)
	obs("F2 then snooping on, suppression unchanged: %s | read enabled=%v suppression=%v", result(err), g.Enabled, g.ReportSuppression)
}

// prioOf is the priority of a port in a QoS reading, or 0 when it is not reported.
func prioOf(q QoS, port int) Priority {
	for _, p := range q.Priorities {
		if p.Port == port {
			return p.Priority
		}
	}
	return 0
}
