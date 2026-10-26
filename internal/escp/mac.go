package escp

import (
	"net"
	"strings"
)

// macLen is the length of an Ethernet MAC address in bytes.
const macLen = 6

// macTextLen is the length of its text form, "aa:bb:cc:dd:ee:ff".
const macTextLen = 3*macLen - 1

// MAC is the hardware address of a switch: the one thing about it that does not change when its
// IP address does. It is a comparable value, so two of them are equal with ==, and the zero value
// means "no MAC" (no device has the all-zero address). Every place that reads, writes, compares or
// prints a switch's MAC goes through this type.
type MAC [macLen]byte

// ParseMAC reads the text form "aa:bb:cc:dd:ee:ff": six hex bytes, any case, separated by colons
// or by hyphens (one kind of separator). Anything else, including the dotted and unseparated forms
// net.ParseMAC accepts, is an error that matches ErrInvalid and shows the form that is accepted.
func ParseMAC(s string) (MAC, error) {
	hw, err := net.ParseMAC(s)
	// net.ParseMAC also reads dotted and unseparated forms and longer addresses; only this form is documented.
	if err != nil || len(hw) != macLen || len(s) != macTextLen || strings.Count(s, ":")+strings.Count(s, "-") != macLen-1 {
		return MAC{}, invalidf("escp: %q is not a MAC address; write it as aa:bb:cc:dd:ee:ff (colons or hyphens)", s)
	}
	return MAC(hw), nil
}

// MACFromBytes converts a six-byte hardware address; ok is false for any other length.
func MACFromBytes(b []byte) (m MAC, ok bool) {
	if len(b) != macLen {
		return MAC{}, false
	}
	return MAC(b), true
}

// IsZero reports whether m is the zero value, "no MAC".
func (m MAC) IsZero() bool { return m == MAC{} }

// String formats the address in lower case with colons, "aa:bb:cc:dd:ee:ff"; the zero value is "".
func (m MAC) String() string {
	if m.IsZero() {
		return ""
	}
	return net.HardwareAddr(m[:]).String()
}
