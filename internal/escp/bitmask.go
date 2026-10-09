package escp

import "encoding/binary"

// portsFromMask expands a big-endian port bitmap (bit n-1 = port n). It reads
// the first maskLen bytes of v; shorter input yields no ports.
func portsFromMask(v []byte) []int {
	if len(v) < maskLen {
		return nil
	}
	m := binary.BigEndian.Uint32(v)
	ports := []int{}
	for i := 0; i < MaxPort; i++ {
		if m&(1<<uint(i)) != 0 {
			ports = append(ports, i+1)
		}
	}
	return ports
}

// maskFromPorts builds the big-endian port bitmap for the given ports.
func maskFromPorts(ports []int) ([]byte, error) {
	var m uint32
	for _, p := range ports {
		if err := validatePort(p); err != nil {
			return nil, err
		}
		m |= 1 << uint(p-1)
	}
	return binary.BigEndian.AppendUint32(nil, m), nil
}
