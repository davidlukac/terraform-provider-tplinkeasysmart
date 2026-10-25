package escptest

import (
	"encoding/binary"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// LinkSpeed is the actual speed code of a port that has a link, as the switch reports it
// (0 means no link).
type LinkSpeed byte

// Link speeds a model port can have.
const (
	Link100Full  LinkSpeed = 5
	Link1000Full LinkSpeed = 6
)

// The refusals of a write that were provoked on a real switch (firmware 20250710), and the
// speed codes they depend on.
const (
	refusedPortRange   int32 = 4
	refusedPortVLAN    int32 = 7401
	refusedBandwidth   int32 = 7403
	refusedStorm       int32 = 7404
	refusedMirrorDest  int32 = 7205
	refusedMirrorSrc   int32 = 7206
	refusedSpeed       int32 = 7406
	refusedHalfDuplex  int32 = 7407
	refusedNegotiation int32 = 7410
	refusedSinglePort  int32 = 7415
	refusedFlowControl int32 = 7417
	refusedEnabled     int32 = 7418

	speedAuto      = 1
	speedHalf10    = 2
	speedHalf100   = 4
	modelPortCount = 8
	defaultVLAN    = 1
	lagGroupSize   = 4
	qosModeDSCP    = 2
	rateRowLen     = 6 // port(1) enabled(1) rate(4)
	stormRowLen    = 9 // port(1) enabled(1) unknown-unicast(1) multicast(1) broadcast(1) rate(4)
	mirrorLen      = 10
)

// EnableModel turns the switch into a model of a freshly reset TL-SG108E: eight ports on auto
// speed (those in links have a link at that speed), no LAG, every port in the default
// port-based VLAN, IGMP and loop prevention on, QoS in DSCP mode, no limits and no mirror. It
// also enforces the cross-setting rules the real switch enforces, with the refusal codes
// measured on firmware 20250710: LAG members must agree on speed, flow control and admin
// state and may not be mirrored, limited or in another VLAN; port-based VLAN membership is
// exclusive, and a port left in none returns to VLAN 1.
func (f *Switch) EnableModel(links map[int]LinkSpeed) {
	f.EnableState(
		Setting{Type: wire.TLVIGMPEnabled, Value: []byte{1}},
		Setting{Type: wire.TLVIGMPGroups},
		Setting{Type: wire.TLVIGMPSuppression, Value: []byte{0}},
		Setting{Type: wire.TLVLoopPrevention, Value: []byte{1}},
		Setting{Type: wire.TLVQoSMode, Value: []byte{qosModeDSCP}},
		Setting{Type: wire.TLVMirror, Value: make([]byte, mirrorLen)},
		Setting{Type: wire.TLVVLANPortBased, Value: []byte{1}},
		Setting{Type: wire.TLVVLANPortBasedMax, Value: []byte{modelPortCount}},
	)
	f.mu.Lock()
	f.bundles[wire.TLVVLANPortBased] = []int16{wire.TLVVLANPortBasedMax, wire.TLVVLANPortBasedVID}
	f.model = true
	f.mu.Unlock()
	var ports, prio, bwIn, bwOut, storm [][]byte
	for p := 1; p <= modelPortCount; p++ {
		ports = append(ports, []byte{byte(p), 1, 0, speedAuto, byte(links[p]), 0, 0})
		prio = append(prio, []byte{byte(p), 0})
		bwIn = append(bwIn, append([]byte{byte(p)}, make([]byte, rateRowLen-1)...))
		bwOut = append(bwOut, append([]byte{byte(p)}, make([]byte, rateRowLen-1)...))
		storm = append(storm, append([]byte{byte(p)}, make([]byte, stormRowLen-1)...))
	}
	f.EnableRows(wire.TLVPorts, ports...)
	f.EnableRows(wire.TLVQoSPriority, prio...)
	f.EnableRows(wire.TLVBandwidthIngress, bwIn...)
	f.EnableRows(wire.TLVBandwidthEgress, bwOut...)
	f.EnableRows(wire.TLVStormControl, storm...)
	f.EnableRows(wire.TLVLAG, make([]byte, 5), make([]byte, 5))
	f.mu.Lock()
	f.rows[wire.TLVLAG][0][0], f.rows[wire.TLVLAG][1][0] = 1, 2
	f.mu.Unlock()
	f.EnableRows(wire.TLVVLANPortBasedVID, binary.BigEndian.AppendUint32([]byte{defaultVLAN}, 1<<modelPortCount-1))
}

// portsOf returns the ports named by a 4-byte mask.
func portsOf(mask []byte) []int {
	var out []int
	m := binary.BigEndian.Uint32(mask)
	for i := 0; i < modelPortCount; i++ {
		if m&(1<<uint(i)) != 0 {
			out = append(out, i+1)
		}
	}
	return out
}

func (f *Switch) row(typ int16, port int) []byte {
	for _, r := range f.rows[typ] {
		if int(r[0]) == port {
			return r
		}
	}
	return nil
}

// refuse returns the refusal code of the first TLV of a SET the model would not accept, or 0.
// The caller holds no lock.
func (f *Switch) refuse(tlvs []wire.TLV) int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.model {
		return 0
	}
	for _, t := range tlvs {
		switch t.Type {
		case wire.TLVLAG:
			if code := f.refuseLAG(t.Value); code != 0 {
				return code
			}
		case wire.TLVPorts:
			if r := f.row(wire.TLVPorts, int(t.Value[0])); r != nil && r[portRowLAG] != 0 && isHalf(t.Value[3]) {
				return refusedHalfDuplex
			}
		}
	}
	return 0
}

func isHalf(speed byte) bool { return speed == speedHalf10 || speed == speedHalf100 }

func (f *Switch) refuseLAG(v []byte) int32 {
	group, members := int(v[0]), portsOf(v[1:])
	if len(members) == 0 {
		return 0 // deleting the group
	}
	if len(members) < 2 {
		return refusedSinglePort
	}
	first := (group-1)*lagGroupSize + 1
	var rows [][]byte
	for _, p := range members {
		if p < first || p >= first+lagGroupSize {
			return refusedPortRange
		}
		rows = append(rows, f.row(wire.TLVPorts, p))
	}
	mirror := f.store[wire.TLVMirror]
	for _, r := range rows {
		if isHalf(r[3]) {
			return refusedHalfDuplex
		}
	}
	for _, r := range rows {
		p := int(r[0])
		if (r[3] == speedAuto) != (rows[0][3] == speedAuto) {
			return refusedNegotiation
		}
		if r[3] != rows[0][3] {
			return refusedSpeed
		}
		if r[5] != rows[0][5] {
			return refusedFlowControl
		}
		if r[1] != rows[0][1] {
			return refusedEnabled
		}
		if f.row(wire.TLVBandwidthIngress, p)[1] == 1 || f.row(wire.TLVBandwidthEgress, p)[1] == 1 {
			return refusedBandwidth
		}
		if f.row(wire.TLVStormControl, p)[1] == 1 {
			return refusedStorm
		}
		for _, vl := range f.rows[wire.TLVVLANPortBasedVID] {
			if vl[0] != defaultVLAN && contains(portsOf(vl[1:]), p) {
				return refusedPortVLAN
			}
		}
		if mirror[0] == 1 {
			if int(mirror[1]) == p {
				return refusedMirrorDest
			}
			if contains(portsOf(mirror[2:6]), p) || contains(portsOf(mirror[6:10]), p) {
				return refusedMirrorSrc
			}
		}
	}
	return 0
}

func contains(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// applied updates what a stored table row implies for the other tables.
func (f *Switch) applied(t wire.TLV) {
	if !f.model {
		return
	}
	switch t.Type {
	case wire.TLVLAG:
		group, members := t.Value[0], portsOf(t.Value[1:])
		for _, r := range f.rows[wire.TLVPorts] {
			switch {
			case contains(members, int(r[0])):
				r[portRowLAG] = group
			case r[portRowLAG] == group:
				r[portRowLAG] = 0
			}
		}
	case wire.TLVVLANPortBasedVID:
		id, mine := t.Value[0], binary.BigEndian.Uint32(t.Value[1:])
		var kept [][]byte
		var covered uint32
		for _, r := range f.rows[wire.TLVVLANPortBasedVID] {
			m := binary.BigEndian.Uint32(r[1:])
			if r[0] != id {
				m &^= mine // membership is exclusive
			}
			if m == 0 && r[0] != defaultVLAN {
				continue // an empty group is deleted
			}
			binary.BigEndian.PutUint32(r[1:], m)
			covered |= m
			kept = append(kept, r)
		}
		f.rows[wire.TLVVLANPortBasedVID] = kept
		for _, r := range kept {
			if r[0] == defaultVLAN { // ports left in no VLAN return to VLAN 1
				binary.BigEndian.PutUint32(r[1:], binary.BigEndian.Uint32(r[1:])|(1<<modelPortCount-1)&^covered)
			}
		}
	}
}
