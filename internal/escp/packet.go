package escp

import (
	"encoding/binary"
	"fmt"
)

// Op codes of the 32-byte packet header.
const (
	OpDiscovery = 0
	OpGet       = 1
	OpReply     = 2 // reply to GET / DISCOVERY
	OpSet       = 3 // SET and LOGIN
	OpSetReply  = 4
)

// TLV types used by this client.
const (
	TLVModel    = 1
	TLVHostname = 2
	TLVMAC      = 3
	TLVIP       = 4
	TLVNetmask  = 5
	TLVGateway  = 6
	TLVFirmware = 7
	TLVHardware = 8
	TLVDHCP     = 9
	TLVLED      = 12
	TLVUsername = 512
	TLVPassword = 514
	TLVRSAKey   = 528
)

// ErrCodeKeyAccepted is returned in the reply to a successful RSA key exchange.
const ErrCodeKeyAccepted = 4098

const headerLen = 32

var packetEnd = []byte{0xff, 0xff, 0x00, 0x00}

// TLV is one type-length-value element.
type TLV struct {
	Type  int16
	Value []byte
}

type header struct {
	Op        byte
	SwitchMAC [6]byte
	HostMAC   [6]byte
	Seq       uint16
	ErrCode   int32
	Length    uint16
}

func marshal(h header, tlvs []TLV) []byte {
	payload := []byte{}
	for _, t := range tlvs {
		payload = binary.BigEndian.AppendUint16(payload, uint16(t.Type))
		payload = binary.BigEndian.AppendUint16(payload, uint16(len(t.Value)))
		payload = append(payload, t.Value...)
	}
	payload = append(payload, packetEnd...)
	b := make([]byte, headerLen, headerLen+len(payload))
	b[0], b[1] = 1, h.Op
	copy(b[2:8], h.SwitchMAC[:])
	copy(b[8:14], h.HostMAC[:])
	binary.BigEndian.PutUint16(b[14:], h.Seq)
	binary.BigEndian.PutUint32(b[16:], uint32(h.ErrCode))
	binary.BigEndian.PutUint16(b[20:], uint16(headerLen+len(payload)))
	return append(b, payload...)
}

// looksLikeHeader reports whether a decrypted buffer plausibly is a packet.
func looksLikeHeader(b []byte) bool {
	return len(b) >= headerLen+len(packetEnd) && b[0] == 1 && b[1] < 5 &&
		int(binary.BigEndian.Uint16(b[20:])) == len(b)
}

func unmarshal(b []byte) (header, []TLV, error) {
	var h header
	if !looksLikeHeader(b) {
		return h, nil, fmt.Errorf("escp: not a valid packet (%d bytes)", len(b))
	}
	h.Op = b[1]
	copy(h.SwitchMAC[:], b[2:8])
	copy(h.HostMAC[:], b[8:14])
	h.Seq = binary.BigEndian.Uint16(b[14:])
	h.ErrCode = int32(binary.BigEndian.Uint32(b[16:]))
	h.Length = binary.BigEndian.Uint16(b[20:])
	var tlvs []TLV
	p := b[headerLen:]
	for len(p) >= 4 {
		t := int16(binary.BigEndian.Uint16(p))
		l := int(binary.BigEndian.Uint16(p[2:]))
		if t == -1 && l == 0 {
			break
		}
		if 4+l > len(p) {
			return h, nil, fmt.Errorf("escp: truncated TLV type %d", t)
		}
		tlvs = append(tlvs, TLV{Type: t, Value: p[4 : 4+l]})
		p = p[4+l:]
	}
	return h, tlvs, nil
}

func cstring(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
