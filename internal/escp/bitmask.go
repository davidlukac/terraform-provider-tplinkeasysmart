package escp

// portsFromMask expands a 32-bit big-endian port bitmap (bit n-1 = port n).
func portsFromMask(v []byte) []int {
	if len(v) < 4 {
		return nil
	}
	m := uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
	ports := []int{}
	for i := 0; i < 32; i++ {
		if m&(1<<uint(i)) != 0 {
			ports = append(ports, i+1)
		}
	}
	return ports
}
