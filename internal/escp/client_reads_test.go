package escp

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// th builds a TLV from a hex value; the values below are the captures used by
// the parser tests (TL-SG108E v6, fw 20230218).
func th(typ int16, h string) TLV {
	b, err := hex.DecodeString(h)
	if err != nil {
		panic(err)
	}
	return TLV{Type: typ, Value: b}
}

// readCase scripts the switch's answers per requested TLV type and states which
// GET requests, in which order, a client method must send.
type readCase struct {
	name     string
	script   map[int16][]TLV
	requests [][]int16
	call     func(*Client, context.Context) (any, error)
	want     any
	// bad is a script whose reply the parser must reject.
	bad map[int16][]TLV
}

func readCases() []readCase {
	return []readCase{
		{
			name:     "Ports",
			script:   map[int16][]TLV{TLVPorts: {th(TLVPorts, "01010101060000"), th(TLVPorts, "03010001000000")}},
			requests: [][]int16{{TLVPorts}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.Ports(ctx) },
			want: []Port{
				{ID: 1, Enabled: true, LAG: 1, ConfiguredSpeed: "auto", ActualSpeed: "1000Mbps-full"},
				{ID: 3, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"},
			},
			bad: map[int16][]TLV{TLVPorts: {{Type: TLVPorts, Value: []byte{1, 1}}}},
		},
		{
			name:     "PortStatistics",
			script:   map[int16][]TLV{TLVPortStats: {th(TLVPortStats, "0101061434f1b7000000002873fb8a00000000")}},
			requests: [][]int16{{TLVPortStats}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.PortStatistics(ctx) },
			want:     []PortStats{{ID: 1, Enabled: true, Link: "1000Mbps-full", TxGood: 0x1434f1b7, RxGood: 0x2873fb8a}},
			bad:      map[int16][]TLV{TLVPortStats: {{Type: TLVPortStats, Value: []byte{1}}}},
		},
		{
			name:     "LAGs",
			script:   map[int16][]TLV{TLVLAG: {th(TLVLAG, "0100000003"), th(TLVLAG, "0200000000")}},
			requests: [][]int16{{TLVLAG}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.LAGs(ctx) },
			want:     []LAG{{ID: 1, Ports: []int{1, 2}}, {ID: 2, Ports: []int{}}},
			bad:      map[int16][]TLV{TLVLAG: {{Type: TLVLAG, Value: []byte{1}}}},
		},
		{
			name: "IGMP asks for the state and the groups separately",
			script: map[int16][]TLV{
				TLVIGMPEnabled: {th(TLVIGMPEnabled, "01"), th(TLVIGMPSuppression, "00")},
				TLVIGMPGroups:  {th(TLVIGMPGroups, "effffffa000100000009")},
			},
			requests: [][]int16{{TLVIGMPEnabled}, {TLVIGMPGroups}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.IGMP(ctx) },
			want:     IGMP{Enabled: true, Groups: []IGMPGroup{{IP: "239.255.255.250", VLAN: 1, Ports: []int{1, 4}}}},
			bad: map[int16][]TLV{
				TLVIGMPEnabled: {th(TLVIGMPEnabled, "01")},
				TLVIGMPGroups:  {{Type: TLVIGMPGroups, Value: make([]byte, 7)}},
			},
		},
		{
			name:     "LoopPrevention",
			script:   map[int16][]TLV{TLVLoopPrevention: {th(TLVLoopPrevention, "01")}},
			requests: [][]int16{{TLVLoopPrevention}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.LoopPrevention(ctx) },
			want:     true,
			bad:      map[int16][]TLV{TLVLoopPrevention: nil},
		},
		{
			name:     "PortMirror",
			script:   map[int16][]TLV{TLVMirror: {th(TLVMirror, "01080000000300000004")}},
			requests: [][]int16{{TLVMirror}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.PortMirror(ctx) },
			want:     Mirror{Enabled: true, MirrorPort: 8, IngressPorts: []int{1, 2}, EgressPorts: []int{3}},
			bad:      map[int16][]TLV{TLVMirror: {{Type: TLVMirror, Value: []byte{1, 2, 3}}}},
		},
		{
			name:     "MTUVLAN",
			script:   map[int16][]TLV{TLVVLANMTU: {th(TLVVLANMTU, "0108")}},
			requests: [][]int16{{TLVVLANMTU}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.MTUVLAN(ctx) },
			want:     MTUVLAN{Enabled: true, UplinkPort: 8},
			bad:      map[int16][]TLV{TLVVLANMTU: nil},
		},
		{
			name: "PortBasedVLAN",
			script: map[int16][]TLV{TLVVLANPortBased: {
				th(TLVVLANPortBased, "01"), th(TLVVLANPortBasedVID, "01000000ff"), th(TLVVLANPortBasedMax, "08")}},
			requests: [][]int16{{TLVVLANPortBased}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.PortBasedVLAN(ctx) },
			want:     PortBasedVLAN{Enabled: true, MaxVLAN: 8, VLANs: []PortVLAN{{ID: 1, Ports: []int{1, 2, 3, 4, 5, 6, 7, 8}}}},
			bad: map[int16][]TLV{TLVVLANPortBased: {
				th(TLVVLANPortBased, "01"), {Type: TLVVLANPortBasedVID, Value: []byte{1}}}},
		},
		{
			name: "VLAN8021Q asks for the VLAN table and the PVIDs separately",
			script: map[int16][]TLV{
				TLVVLAN8021QVLAN: {th(TLVVLAN8021Q, "00"), th(TLVVLAN8021QMax, "20")},
				TLVVLAN8021QPVID: {th(TLVVLAN8021QPVID, "010001")},
			},
			requests: [][]int16{{TLVVLAN8021QVLAN}, {TLVVLAN8021QPVID}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.VLAN8021Q(ctx) },
			want:     Dot1Q{MaxVLAN: 32, VLANs: []VLAN8021Q{}, PVIDs: []PortPVID{{Port: 1, PVID: 1}}},
			bad: map[int16][]TLV{
				TLVVLAN8021QVLAN: {th(TLVVLAN8021Q, "01"), {Type: TLVVLAN8021QVLAN, Value: []byte{0, 1}}},
				TLVVLAN8021QPVID: nil,
			},
		},
		{
			name: "QoS asks for the mode and the priorities separately",
			script: map[int16][]TLV{
				TLVQoSMode:     {th(TLVQoSMode, "02")},
				TLVQoSPriority: {th(TLVQoSPriority, "0100"), th(TLVQoSPriority, "0203")},
			},
			requests: [][]int16{{TLVQoSMode}, {TLVQoSPriority}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.QoS(ctx) },
			want:     QoS{Mode: 2, ModeName: "dscp-802.1p", Priorities: []PortPriority{{1, 1}, {2, 4}}},
			bad: map[int16][]TLV{
				TLVQoSMode:     {th(TLVQoSMode, "02")},
				TLVQoSPriority: {{Type: TLVQoSPriority, Value: []byte{1}}},
			},
		},
		{
			name: "Bandwidth asks for ingress and egress separately",
			script: map[int16][]TLV{
				TLVBandwidthIngress: {th(TLVBandwidthIngress, "020000001000")},
				TLVBandwidthEgress:  {th(TLVBandwidthEgress, "020000002000")},
			},
			requests: [][]int16{{TLVBandwidthIngress}, {TLVBandwidthEgress}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.Bandwidth(ctx) },
			want:     []PortBandwidth{{Port: 2, IngressKbps: 0x1000, EgressKbps: 0x2000}},
			bad: map[int16][]TLV{
				TLVBandwidthIngress: {{Type: TLVBandwidthIngress, Value: []byte{1}}},
				TLVBandwidthEgress:  nil,
			},
		},
		{
			name:     "StormControl",
			script:   map[int16][]TLV{TLVStormControl: {th(TLVStormControl, "0200010100"+"00001388")}},
			requests: [][]int16{{TLVStormControl}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.StormControl(ctx) },
			want:     []PortStorm{{Port: 2, UnknownUnicast: true, Multicast: true, RateKbps: 0x1388, Enabled: true}},
			bad:      map[int16][]TLV{TLVStormControl: {{Type: TLVStormControl, Value: []byte{1}}}},
		},
		{
			name: "System asks for the port count and auto-save separately",
			script: map[int16][]TLV{
				TLVPortCount: {th(TLVPortCount, "08")},
				TLVAutoSave:  {th(TLVAutoSave, "01")},
			},
			requests: [][]int16{{TLVPortCount}, {TLVAutoSave}},
			call:     func(c *Client, ctx context.Context) (any, error) { return c.System(ctx) },
			want:     System{PortCount: 8, AutoSave: true},
			bad: map[int16][]TLV{
				TLVPortCount: nil,
				TLVAutoSave:  {th(TLVAutoSave, "01")},
			},
		},
	}
}

// scriptedGETs records the GET requests the fake receives and answers them
// from a script.
type scriptedGETs struct {
	mu       sync.Mutex
	requests [][]int16
}

func (s *scriptedGETs) snapshot() [][]int16 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]int16(nil), s.requests...)
}

// install makes the fake answer GETs from script. A request whose types equal
// failOn is answered with failCode instead, or not at all when drop is set.
func (s *scriptedGETs) install(f *fakeSwitch, script map[int16][]TLV, failOn []int16, failCode int32, drop bool) {
	f.configure(func(f *fakeSwitch) {
		f.getFn = func(types []int16) ([]TLV, int32, bool) {
			s.mu.Lock()
			s.requests = append(s.requests, append([]int16(nil), types...))
			s.mu.Unlock()
			if failOn != nil && reflect.DeepEqual(types, failOn) {
				return nil, failCode, drop
			}
			var out []TLV
			for _, t := range types {
				out = append(out, script[t]...)
			}
			return out, 0, false
		}
	})
}

func TestReadMethodsSendTheExpectedRequestsAndDecodeTheReplies(t *testing.T) {
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeSwitch(t)
			c := dialFast(t, f, "secret")
			var log scriptedGETs
			log.install(f, tc.script, nil, 0, false)
			got, err := tc.call(c, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decoded value\n got %+v\nwant %+v", got, tc.want)
			}
			if got := log.snapshot(); !reflect.DeepEqual(got, tc.requests) {
				t.Fatalf("GET requests\n got %v\nwant %v", got, tc.requests)
			}
		})
	}
}

func TestReadMethodsReportEachFailingRequestStage(t *testing.T) {
	for _, tc := range readCases() {
		for stage, failOn := range tc.requests {
			stageName := tc.name + " request " + string(rune('1'+stage))
			t.Run(stageName+" unanswered", func(t *testing.T) {
				f := startFakeSwitch(t)
				c := dialFast(t, f, "secret")
				var log scriptedGETs
				log.install(f, tc.script, failOn, 0, true)
				if _, err := tc.call(c, context.Background()); !errors.Is(err, ErrNoReply) {
					t.Fatalf("want ErrNoReply, got %v", err)
				}
			})
			t.Run(stageName+" answered with an error code", func(t *testing.T) {
				f := startFakeSwitch(t)
				c := dialFast(t, f, "secret")
				var log scriptedGETs
				log.install(f, tc.script, failOn, 7, false)
				_, err := tc.call(c, context.Background())
				if err == nil || !strings.Contains(err.Error(), "error code 7") {
					t.Fatalf("want an error naming code 7, got %v", err)
				}
				if errors.Is(err, ErrNoReply) {
					t.Fatal("an answered request must not be reported as silence")
				}
			})
		}
	}
}

func TestReadMethodsSurfaceParserErrors(t *testing.T) {
	// A reply the parser rejects must be returned as an error, never turned
	// into a partial value.
	for _, tc := range readCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := startFakeSwitch(t)
			c := dialFast(t, f, "secret")
			var log scriptedGETs
			log.install(f, tc.bad, nil, 0, false)
			got, err := tc.call(c, context.Background())
			if err == nil {
				t.Fatalf("a malformed reply was accepted and decoded as %+v", got)
			}
			if errors.Is(err, ErrNoReply) {
				t.Fatalf("the switch did answer; the error must come from decoding, got %v", err)
			}
		})
	}
}
