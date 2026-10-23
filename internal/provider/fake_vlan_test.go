package provider

import (
	"context"
	"sort"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// fakePortCount is the number of ports of the fake switch's 802.1Q default VLAN.
const fakePortCount = 8

// fakeVLANs models what the real switch does with the three VLAN modes and
// the 802.1Q table, as far as it was observed: the modes exclude each other,
// enabling 802.1Q creates VLAN 1 with every port untagged and PVID 1, and a
// PVID can only point to an existing VLAN.
type fakeVLANs struct {
	mtu       escp.MTUVLAN
	portBased bool
	enabled   bool // 802.1Q mode
	vlans     map[int]escp.VLAN8021Q
	pvids     map[int]int

	ignoreModeWrites bool // accept mode writes without applying them
}

// newFakeVLANs returns a switch in the given mode ("mtu", "port-based", "802.1q" or "").
func newFakeVLANs(mode string) *fakeVLANs {
	v := &fakeVLANs{mtu: escp.MTUVLAN{UplinkPort: 1}, vlans: map[int]escp.VLAN8021Q{}, pvids: map[int]int{}}
	switch mode {
	case "mtu":
		v.mtu = escp.MTUVLAN{Enabled: true, UplinkPort: 8}
	case "port-based":
		v.portBased = true
	case "802.1q":
		v.enable8021Q()
	}
	return v
}

func allFakePorts() []int {
	ports := make([]int, 0, fakePortCount)
	for p := 1; p <= fakePortCount; p++ {
		ports = append(ports, p)
	}
	return ports
}

// enable8021Q turns the mode on and builds the default VLAN 1 with every port untagged.
func (v *fakeVLANs) enable8021Q() {
	v.enabled, v.portBased, v.mtu = true, false, escp.MTUVLAN{UplinkPort: 1}
	v.vlans = map[int]escp.VLAN8021Q{1: {ID: 1, Name: "Default", Untagged: allFakePorts(), Tagged: []int{}}}
	v.pvids = map[int]int{}
	for _, p := range allFakePorts() {
		v.pvids[p] = 1
	}
}

// dot1q is the 802.1Q state as the switch reports it, VLANs and PVIDs sorted.
func (v *fakeVLANs) dot1q() escp.Dot1Q {
	out := escp.Dot1Q{Enabled: v.enabled, MaxVLAN: 32, VLANs: []escp.VLAN8021Q{}, PVIDs: []escp.PortPVID{}}
	ids := make([]int, 0, len(v.vlans))
	for id := range v.vlans {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		out.VLANs = append(out.VLANs, v.vlans[id])
	}
	for _, p := range allFakePorts() {
		if pvid, ok := v.pvids[p]; ok {
			out.PVIDs = append(out.PVIDs, escp.PortPVID{Port: p, PVID: pvid})
		}
	}
	return out
}

// write runs a fake write under the lock, counts it, and applies the injected failure.
func (f *fakeClient) vlanWrite(do func(v *fakeVLANs) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSet != nil {
		return f.failSet
	}
	f.sets++
	return do(f.vlans)
}

// VLANMode names the mode the way the library does, from the fake's three flags.
func (f *fakeClient) VLANMode(ctx context.Context) (escp.VLANModeSetting, error) {
	mtu, _ := f.MTUVLAN(ctx)
	pb, _ := f.PortBasedVLAN(ctx)
	q, _ := f.Dot1Q(ctx)
	return escp.VLANModeFrom(mtu, pb.Enabled, q.Enabled), nil
}

func (f *fakeClient) SetVLANMode(_ context.Context, s escp.VLANModeSetting) error {
	if err := escp.ValidateVLANMode(s); err != nil {
		return err
	}
	return f.vlanWrite(func(v *fakeVLANs) error {
		switch {
		case v.ignoreModeWrites:
		case s.Mode == escp.VLANModeMTU:
			v.mtu, v.enabled, v.portBased = escp.MTUVLAN{Enabled: true, UplinkPort: s.UplinkPort}, false, false
		case s.Mode == escp.VLANModePortBased:
			v.portBased, v.enabled, v.mtu = true, false, escp.MTUVLAN{UplinkPort: 1}
		default:
			v.enable8021Q()
		}
		return nil
	})
}

func (f *fakeClient) SetVLAN8021Q(_ context.Context, vlan escp.VLAN8021Q) error {
	return f.vlanWrite(func(v *fakeVLANs) error {
		v.vlans[vlan.ID] = vlan
		return nil
	})
}

func (f *fakeClient) DeleteVLAN8021Q(_ context.Context, id int) error {
	return f.vlanWrite(func(v *fakeVLANs) error {
		delete(v.vlans, id)
		return nil
	})
}

func (f *fakeClient) SetPVIDs(_ context.Context, pvids map[int]int) error {
	return f.vlanWrite(func(v *fakeVLANs) error {
		for port, pvid := range pvids {
			v.pvids[port] = pvid
		}
		return nil
	})
}
