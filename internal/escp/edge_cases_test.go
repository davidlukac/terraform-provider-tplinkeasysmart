package escp

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Parsers receive replies that may carry TLV types they do not own (a GET of
// several types, or a switch answering more than asked). They must ignore them
// and still decode what is theirs.
func TestParsersIgnoreForeignTLVs(t *testing.T) {
	foreign := TLV{Type: 9999, Value: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}}
	tests := []struct {
		name string
		got  func() (any, error)
		want any
	}{
		{"ports", func() (any, error) { return parsePorts([]TLV{foreign, th(TLVPorts, "03010001000000")}) },
			[]Port{{ID: 3, Enabled: true, ConfiguredSpeed: "auto", ActualSpeed: "link-down"}}},
		{"port statistics", func() (any, error) { return parsePortStats([]TLV{foreign}) }, []PortStats(nil)},
		{"LAGs", func() (any, error) { return parseLAGs([]TLV{foreign, th(TLVLAG, "0100000003")}) },
			[]LAG{{ID: 1, Ports: []int{1, 2}}}},
		{"storm control", func() (any, error) { return parseStorm([]TLV{foreign}) }, []PortStorm(nil)},
		{"bandwidth", func() (any, error) { return parseBandwidth([]TLV{foreign, th(TLVBandwidthEgress, "010000000040")}) },
			[]PortBandwidth{{Port: 1, EgressKbps: 0x40}}},
		{"mirror", func() (any, error) { return parseMirror([]TLV{foreign, th(TLVMirror, "00000000000000000000")}) },
			Mirror{IngressPorts: []int{}, EgressPorts: []int{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.got()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParsersRejectMalformedState(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantMsg string
	}{
		{"mirror without a TLV", second(parseMirror(nil)), "no port mirror TLV"},
		{"QoS mode with two bytes", second(parseQoS([]TLV{{Type: TLVQoSMode, Value: []byte{1, 2}}})), "QoS mode TLV has 2 bytes"},
		{"QoS without any mode", second(parseQoS([]TLV{th(TLVQoSPriority, "0100")})), "no QoS mode"},
		{"802.1Q state with two bytes", second(parseDot1Q([]TLV{{Type: TLVVLAN8021Q, Value: []byte{0, 0}}})), "802.1Q state TLV has 2 bytes"},
		{"802.1Q without any state", second(parseDot1Q(nil)), "no 802.1Q state"},
		{"port-based VLAN state with two bytes", second(parsePortBasedVLAN([]TLV{{Type: TLVVLANPortBased, Value: []byte{1, 1}}})), "port-based VLAN state TLV has 2 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil || !strings.Contains(tt.err.Error(), tt.wantMsg) {
				t.Fatalf("want an error containing %q, got %v", tt.wantMsg, tt.err)
			}
		})
	}
}

// second returns the error of a (value, error) pair.
func second[T any](_ T, err error) error { return err }

func TestSpeedNameUnknownCode(t *testing.T) {
	if got := speedName(9); got != "unknown(9)" {
		t.Fatalf("got %q", got)
	}
	if got := speedName(0); got != "link-down" {
		t.Fatalf("got %q", got)
	}
}

func TestPortsFromMask(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []int
	}{
		{"too short is nothing", []byte{0xff, 0xff}, nil},
		{"empty is nothing", nil, nil},
		{"no bits", []byte{0, 0, 0, 0}, []int{}},
		{"lowest bit is port 1", []byte{0, 0, 0, 1}, []int{1}},
		{"highest bit is port 32", []byte{0x80, 0, 0, 0}, []int{32}},
		{"ports 1 to 8", []byte{0, 0, 0, 0xff}, []int{1, 2, 3, 4, 5, 6, 7, 8}},
		{"longer input only uses four bytes", []byte{0, 0, 0, 2, 0xff}, []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := portsFromMask(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestMaskFromPorts(t *testing.T) {
	got, err := maskFromPorts([]int{1, 8, 32})
	if err != nil || !bytes.Equal(got, []byte{0x80, 0, 0, 0x81}) {
		t.Fatalf("got %x, %v", got, err)
	}
	for _, bad := range [][]int{{0}, {33}, {1, -1}} {
		if _, err := maskFromPorts(bad); err == nil {
			t.Errorf("%v must be rejected", bad)
		}
	}
}

func TestUnmarshalRejectsPacketsThatLieAboutTheirStructure(t *testing.T) {
	good := marshal(header{Op: OpReply, Seq: 7}, []TLV{{Type: 5, Value: []byte{1, 2}}})
	if _, tlvs, err := unmarshal(good); err != nil || len(tlvs) != 1 {
		t.Fatalf("control packet must parse: %v %v", tlvs, err)
	}
	t.Run("TLV longer than the packet", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[headerLen+2], bad[headerLen+3] = 0, 100 // claims 100 value bytes, 2 present
		if _, _, err := unmarshal(bad); err == nil || !strings.Contains(err.Error(), "truncated TLV type 5") {
			t.Fatalf("want a truncation error, got %v", err)
		}
	})
	t.Run("too short for a header", func(t *testing.T) {
		if _, _, err := unmarshal(good[:20]); err == nil || !strings.Contains(err.Error(), "not a valid packet") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("declared length does not match", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[21]++
		if _, _, err := unmarshal(bad); err == nil {
			t.Fatal("a wrong total length must be rejected")
		}
	})
	t.Run("unknown version", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[0] = 2
		if _, _, err := unmarshal(bad); err == nil {
			t.Fatal("version 2 must be rejected")
		}
	})
	t.Run("opcode out of range", func(t *testing.T) {
		bad := append([]byte(nil), good...)
		bad[1] = 5
		if _, _, err := unmarshal(bad); err == nil {
			t.Fatal("opcode 5 must be rejected")
		}
	})
}

func TestCString(t *testing.T) {
	for in, want := range map[string]string{"abc\x00def": "abc", "abc": "abc", "": "", "\x00": ""} {
		if got := cstring([]byte(in)); got != want {
			t.Errorf("cstring(%q) = %q, want %q", in, got, want)
		}
	}
}

// A ciphertext whose most significant words are zero is sent with a smaller
// word count, exactly like the vendor utility. The key was found by search and
// the expected TLV is cross-checked by decrypting it with the private key.
func TestRSASessionTLVTrimsLeadingZeroWords(t *testing.T) {
	key := []byte("AAAAahqn")
	want, _ := hex.DecodeString("0003569d648da2df0000")
	got := RSASessionTLV(key)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if got[1] != 3 {
		t.Fatalf("word count must be trimmed to 3, got %d", got[1])
	}
	f := &fakeSwitch{}
	if back := f.decryptSessionKey(got); !bytes.Equal(back, key) {
		t.Fatalf("the trimmed TLV must still decrypt to the key, got %q", back)
	}
}

func TestBandwidthValidationHappensBeforeAnythingIsSent(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFast(t, f, "secret")
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"port 0":                   func() error { return c.SetBandwidth(ctx, 0, 64, 64) },
		"port 33":                  func() error { return c.SetBandwidth(ctx, 33, 64, 64) },
		"bad ingress":              func() error { return c.SetBandwidth(ctx, 7, 100, 64) },
		"bad egress after good in": func() error { return c.SetBandwidth(ctx, 7, 64, 100) },
	} {
		if err := call(); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if n := len(f.allSets()); n != 0 {
		t.Fatalf("an invalid egress rate must stop the request before the ingress SET is sent, %d SET(s) reached the switch", n)
	}
}

func TestBandwidthPartialFailureIsReportedAndNotHidden(t *testing.T) {
	t.Run("ingress rejected: nothing applied", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.configure(func(f *fakeSwitch) { f.rejectSetsFrom = 1 })
		c := dialFast(t, f, "secret")
		if err := c.SetBandwidth(context.Background(), 7, 64, 64); !errors.Is(err, ErrAuth) {
			t.Fatalf("got %v", err)
		}
		if n := len(f.allSets()); n != 0 {
			t.Fatalf("%d SET(s) applied", n)
		}
	})
	t.Run("egress rejected: ingress was applied and the error says so", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.configure(func(f *fakeSwitch) { f.rejectSetsFrom = 2 })
		c := dialFast(t, f, "secret")
		if err := c.SetBandwidth(context.Background(), 7, 64, 64); !errors.Is(err, ErrAuth) {
			t.Fatalf("got %v", err)
		}
		if h := f.allSets(); len(h) != 1 || h[0][0].Type != TLVBandwidthIngress {
			t.Fatalf("exactly the ingress SET should have been applied: %+v", h)
		}
	})
}

func TestStormControlRejectsInvalidPortBeforeSending(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFast(t, f, "secret")
	for _, port := range []int{0, 33} {
		if err := c.SetStormControl(context.Background(), port, true, false, false, 64); err == nil {
			t.Errorf("port %d must be rejected", port)
		}
	}
	if n := len(f.allSets()); n != 0 {
		t.Fatalf("%d SET(s) reached the switch", n)
	}
}

func TestMirrorRejectsInvalidSourcePortsBeforeSending(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFast(t, f, "secret")
	ctx := context.Background()
	for name, m := range map[string]Mirror{
		"invalid ingress": {Enabled: true, MirrorPort: 7, IngressPorts: []int{0}},
		"invalid egress":  {Enabled: true, MirrorPort: 7, IngressPorts: []int{4}, EgressPorts: []int{40}},
	} {
		if err := c.SetPortMirror(ctx, m); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	if n := len(f.allSets()); n != 0 {
		t.Fatalf("%d SET(s) reached the switch", n)
	}
}

func TestSetPortPrioritiesStopsWhenTheCurrentStateCannotBeRead(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFast(t, f, "secret")
	f.configure(func(f *fakeSwitch) { f.getFn = func([]int16) ([]TLV, int32, bool) { return nil, 0, true } })
	if err := c.SetPortPriorities(context.Background(), map[int]int{3: 2}); !errors.Is(err, ErrNoReply) {
		t.Fatalf("got %v", err)
	}
	if n := len(f.allSets()); n != 0 {
		t.Fatalf("priorities must not be written blind: %d SET(s)", n)
	}
}

func TestSetHostnameRefusesToWriteWithoutAUsableIPConfiguration(t *testing.T) {
	t.Run("switch does not answer discovery", func(t *testing.T) {
		f := startFakeSwitch(t)
		f.goMute()
		c := dialFast(t, f, "secret")
		if err := c.SetHostname(context.Background(), "new-name"); !errors.Is(err, ErrNoReply) {
			t.Fatalf("got %v", err)
		}
	})
	bad := map[string][]TLV{
		"missing IP":      {{Type: TLVNetmask, Value: []byte{255, 255, 255, 0}}, {Type: TLVGateway, Value: []byte{10, 0, 0, 1}}},
		"missing netmask": {{Type: TLVIP, Value: []byte{10, 0, 0, 5}}, {Type: TLVGateway, Value: []byte{10, 0, 0, 1}}},
		"missing gateway": {{Type: TLVIP, Value: []byte{10, 0, 0, 5}}, {Type: TLVNetmask, Value: []byte{255, 255, 255, 0}}},
		"malformed IP":    {{Type: TLVIP, Value: []byte{10, 0, 5}}, {Type: TLVNetmask, Value: []byte{255, 255, 255, 0}}, {Type: TLVGateway, Value: []byte{10, 0, 0, 1}}},
	}
	for name, tlvs := range bad {
		t.Run(name, func(t *testing.T) {
			f := startFakeSwitch(t)
			f.configure(func(f *fakeSwitch) { f.discoverTLVs = tlvs })
			c := dialFast(t, f, "secret")
			err := c.SetHostname(context.Background(), "new-name")
			if err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
				t.Fatalf("want a refusal, got %v", err)
			}
			if n := len(f.allSets()); n != 0 {
				t.Fatalf("the management IP must never be written from a half-known state, %d SET(s) sent", n)
			}
		})
	}
}
