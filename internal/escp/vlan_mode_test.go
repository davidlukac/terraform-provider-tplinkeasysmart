package escp

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestVLANModeFrom(t *testing.T) {
	on := MTUVLAN{Enabled: true, UplinkPort: 8}
	off := MTUVLAN{UplinkPort: 1} // a disabled MTU VLAN reports uplink 1, which must not leak into the result
	cases := []struct {
		name      string
		mtu       MTUVLAN
		portBased bool
		dot1q     bool
		want      VLANModeSetting
	}{
		{"MTU VLAN with its uplink", on, false, false, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 8}},
		{"port-based", off, true, false, VLANModeSetting{Mode: VLANModePortBased}},
		{"802.1Q", off, false, true, VLANModeSetting{Mode: VLANModeDot1Q}},
		{"no mode at all", off, false, false, VLANModeSetting{Mode: VLANModeNone}},
		{"MTU and port-based", on, true, false, VLANModeSetting{Mode: VLANModeNone}},
		{"MTU and 802.1Q", on, false, true, VLANModeSetting{Mode: VLANModeNone}},
		{"port-based and 802.1Q", off, true, true, VLANModeSetting{Mode: VLANModeNone}},
		{"all three", on, true, true, VLANModeSetting{Mode: VLANModeNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := VLANModeFrom(tc.mtu, tc.portBased, tc.dot1q); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestVLANModesAreTheWritableOnes(t *testing.T) {
	want := []VLANMode{VLANModeMTU, VLANModePortBased, VLANModeDot1Q}
	if got := VLANModes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// the spellings are part of the Terraform provider's state
	if VLANModeMTU != "mtu" || VLANModePortBased != "port-based" || VLANModeDot1Q != "802.1q" || VLANModeNone != "none" {
		t.Fatal("a VLAN mode changed its spelling")
	}
}

// scriptModes makes the fake report the three mode flags the way a switch does.
func scriptModes(f *escptest.Switch, mtu, portBased, dot1q bool, silentOn int16) {
	flag := func(b bool) string {
		if b {
			return "01"
		}
		return "00"
	}
	f.Configure(func(f *escptest.Switch) {
		f.GetFn = func(types []int16) ([]escptest.Setting, int32, bool) {
			var out []escptest.Setting
			for _, typ := range types {
				if typ == silentOn {
					return nil, 0, true
				}
				switch typ {
				case wire.TLVVLANMTU:
					out = append(out, escptest.Setting{Type: wire.TLVVLANMTU, Value: []byte{map[bool]byte{true: 1}[mtu], 8}})
				case wire.TLVVLANPortBased:
					for _, x := range []wire.TLV{th(wire.TLVVLANPortBased, flag(portBased)), th(wire.TLVVLANPortBasedVID, "01000000ff"), th(wire.TLVVLANPortBasedMax, "08")} {
						out = append(out, escptest.Setting{Type: x.Type, Value: x.Value})
					}
				case wire.TLVVLAN8021QVLAN:
					for _, x := range []wire.TLV{th(wire.TLVVLAN8021Q, flag(dot1q)), th(wire.TLVVLAN8021QMax, "20")} {
						out = append(out, escptest.Setting{Type: x.Type, Value: x.Value})
					}
				case wire.TLVVLAN8021QPVID:
					out = append(out, escptest.Setting{Type: wire.TLVVLAN8021QPVID, Value: []byte{1, 0, 1}})
				}
			}
			return out, 0, false
		}
	})
}

func TestVLANModeReadsTheThreeFlags(t *testing.T) {
	cases := []struct {
		name                string
		mtu, portBased, dot bool
		want                VLANModeSetting
	}{
		{"MTU", true, false, false, VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 8}},
		{"port-based", false, true, false, VLANModeSetting{Mode: VLANModePortBased}},
		{"802.1Q", false, false, true, VLANModeSetting{Mode: VLANModeDot1Q}},
		{"none", false, false, false, VLANModeSetting{Mode: VLANModeNone}},
		{"two at once", false, true, true, VLANModeSetting{Mode: VLANModeNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			scriptModes(f, tc.mtu, tc.portBased, tc.dot, 0)
			c := dialFake(t, f, "secret")
			got, err := c.VLANMode(context.Background())
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
			if n := f.WriteCount(); n != 0 {
				t.Fatalf("a read wrote %d times", n)
			}
		})
	}
}

// Each of the three reads can fail, and the failure is the answer: no guess from two flags.
func TestVLANModeFailsWhenAnyReadFails(t *testing.T) {
	for name, typ := range map[string]int16{"MTU": wire.TLVVLANMTU, "port-based": wire.TLVVLANPortBased, "802.1Q": wire.TLVVLAN8021QVLAN} {
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			scriptModes(f, false, true, false, typ)
			c := dialFast(t, f, "secret")
			if got, err := c.VLANMode(context.Background()); !errors.Is(err, ErrNoReply) || got != (VLANModeSetting{}) {
				t.Fatalf("got %+v, %v; want ErrNoReply and nothing", got, err)
			}
		})
	}
}

// The wire: each mode is its own TLV with the enabled flag, and the MTU VLAN adds its uplink. These
// bytes are the vendor utility's and are unchanged from the three writers SetVLANMode replaces.
func TestSetVLANModeWire(t *testing.T) {
	cases := []struct {
		name string
		set  VLANModeSetting
		want escptest.Setting
	}{
		{"802.1Q", VLANModeSetting{Mode: VLANModeDot1Q}, escptest.Setting{Type: wire.TLVVLAN8021Q, Value: []byte{1}}},
		{"port-based", VLANModeSetting{Mode: VLANModePortBased}, escptest.Setting{Type: wire.TLVVLANPortBased, Value: []byte{1}}},
		{"MTU, uplink 3", VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 3}, escptest.Setting{Type: wire.TLVVLANMTU, Value: []byte{1, 3}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			if err := c.SetVLANMode(context.Background(), tc.set); err != nil {
				t.Fatal(err)
			}
			if got := f.LastSet(); f.WriteCount() != 1 || len(got) != 1 || !reflect.DeepEqual(got[0], tc.want) {
				t.Fatalf("wire %+v after %d write(s), want exactly %+v", got, f.WriteCount(), tc.want)
			}
		})
	}
}

func TestSetVLANModeRefusesWhatCannotBeSet(t *testing.T) {
	cases := []struct {
		name string
		set  VLANModeSetting
	}{
		{"none cannot be set", VLANModeSetting{Mode: VLANModeNone}},
		{"an empty mode", VLANModeSetting{}},
		{"an unknown mode", VLANModeSetting{Mode: "vxlan"}},
		{"MTU without an uplink", VLANModeSetting{Mode: VLANModeMTU}},
		{"MTU with a port that does not fit a mask", VLANModeSetting{Mode: VLANModeMTU, UplinkPort: 33}},
		{"port-based with an uplink", VLANModeSetting{Mode: VLANModePortBased, UplinkPort: 3}},
		{"802.1Q with an uplink", VLANModeSetting{Mode: VLANModeDot1Q, UplinkPort: 3}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := escptest.Start(t)
			c := dialFake(t, f, "secret")
			err := c.SetVLANMode(context.Background(), tc.set)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
			if err2 := ValidateVLANMode(tc.set); !errors.Is(err2, ErrInvalid) || err2.Error() != err.Error() {
				t.Errorf("ValidateVLANMode must give the same answer: %v", err2)
			}
			if n := f.WriteCount(); n != 0 {
				t.Fatalf("the switch received %d writes", n)
			}
		})
	}
}
