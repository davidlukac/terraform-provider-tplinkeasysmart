package escp

import (
	"encoding/binary"
	"math/bits"
	"strconv"
	"strings"
)

// PortID is the number of a port, counted from 1. The largest a port mask can express is MaxPort;
// how many ports a switch really has is known only to the switch, so writers also refuse ports
// beyond its own count. The zero value means "no port" where a record allows it (a disabled
// mirror has no monitoring port).
type PortID int

// Validate reports whether p can be addressed in a port mask (1 to MaxPort); the error matches
// ErrInvalid.
func (p PortID) Validate() error {
	if p < 1 || p > MaxPort {
		return invalidf("escp: invalid port %d (valid: 1-%d)", int(p), MaxPort)
	}
	return nil
}

// ParsePortID reads a port number written in decimal; the port must be valid (Validate), and the
// error then matches ErrInvalid.
func ParsePortID(s string) (PortID, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, invalidf("escp: %q is not a port number", s)
	}
	p := PortID(n)
	return p, p.Validate()
}

// set is the one-port set; p must be valid.
func (p PortID) set() PortSet { return 1 << uint(p-1) }

// String is the port number.
func (p PortID) String() string { return strconv.Itoa(int(p)) }

// PortSet is a set of ports: unordered, without duplicates, with ports 1 to MaxPort only. It is a
// comparable value (two sets are the same when they are ==) held as the very bitmap that goes on
// the wire (bit n-1 is port n), so building, comparing, sorting and removing duplicates are the
// operations of the type and nowhere else. The zero value is the empty set.
type PortSet uint32

// NewPortSet builds a set from ports; a port outside 1 to MaxPort is an error that matches
// ErrInvalid. Repeating a port is not an error.
func NewPortSet(ports ...PortID) (PortSet, error) {
	var s PortSet
	for _, p := range ports {
		if err := p.Validate(); err != nil {
			return 0, err
		}
		s |= 1 << uint(p-1)
	}
	return s, nil
}

// MustPortSet is NewPortSet for ports known to be valid (constants and tests); it panics otherwise.
func MustPortSet(ports ...PortID) PortSet {
	s, err := NewPortSet(ports...)
	if err != nil {
		panic(err)
	}
	return s
}

// PortSetFromMask reads a big-endian port bitmap (bit n-1 = port n) from the first four bytes of
// v; shorter input is the empty set.
func PortSetFromMask(v []byte) PortSet {
	if len(v) < maskLen {
		return 0
	}
	return PortSet(binary.BigEndian.Uint32(v))
}

// Mask is the big-endian port bitmap that goes on the wire.
func (s PortSet) Mask() []byte { return binary.BigEndian.AppendUint32(nil, uint32(s)) }

// Has reports whether p is in the set.
func (s PortSet) Has(p PortID) bool { return p >= 1 && p <= MaxPort && s&(1<<uint(p-1)) != 0 }

// Len is the number of ports in the set.
func (s PortSet) Len() int { return bits.OnesCount32(uint32(s)) }

// IsEmpty reports whether the set has no port.
func (s PortSet) IsEmpty() bool { return s == 0 }

// Union is the ports in either set.
func (s PortSet) Union(o PortSet) PortSet { return s | o }

// Intersect is the ports in both sets.
func (s PortSet) Intersect(o PortSet) PortSet { return s & o }

// Minus is the ports of s that are not in o.
func (s PortSet) Minus(o PortSet) PortSet { return s &^ o }

// Ports lists the ports in ascending order; the empty set gives a non-nil empty slice.
func (s PortSet) Ports() []PortID {
	out := make([]PortID, 0, s.Len())
	for i := 0; i < MaxPort; i++ {
		if s&(1<<uint(i)) != 0 {
			out = append(out, PortID(i+1))
		}
	}
	return out
}

// Ints lists the ports in ascending order as plain integers, for callers that hold them that way.
func (s PortSet) Ints() []int {
	ports := s.Ports()
	out := make([]int, len(ports))
	for i, p := range ports {
		out[i] = int(p)
	}
	return out
}

// String lists the ports in ascending order, "2,3,5"; the empty set is "none".
func (s PortSet) String() string {
	if s.IsEmpty() {
		return "none"
	}
	parts := make([]string, 0, s.Len())
	for _, p := range s.Ports() {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, ",")
}
