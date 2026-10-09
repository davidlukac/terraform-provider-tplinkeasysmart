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
	TLVModel          = 1
	TLVHostname       = 2
	TLVMAC            = 3
	TLVIP             = 4
	TLVNetmask        = 5
	TLVGateway        = 6
	TLVFirmware       = 7
	TLVHardware       = 8
	TLVDHCP           = 9
	TLVLED            = 12
	TLVFactoryDefault = 14
	TLVConfigFile     = 768 // configuration backup (read through an authenticated SET)
	TLVUsername       = 512
	TLVPassword       = 514
	TLVRSAKey         = 528
)

// ErrCodeKeyAccepted is returned in the reply to a successful RSA key exchange.
const ErrCodeKeyAccepted = 4098

// Layout of the packet header (all fields big-endian).
const (
	protocolVersion = 1  // first header byte
	headerLen       = 32 // total header size
	macLen          = 6  // length of a MAC address
	tlvHeaderLen    = 4  // TLV type (2 bytes) + length (2 bytes)

	offOp        = 1  // operation code
	offSwitchMAC = 2  // switch MAC
	offHostMAC   = 8  // host MAC
	offSeq       = 14 // sequence number
	offErrCode   = 16 // error code
	offLength    = 20 // total packet length
	offFragment  = 22 // non-zero while more fragments of the reply follow
)

// packetEnd terminates the TLV list: type -1 (0xffff), length 0.
var packetEnd = []byte{0xff, 0xff, 0x00, 0x00}

// tlvEndType is the type of the terminating TLV.
const tlvEndType = -1

// TLV is one type-length-value element.
type TLV struct {
	Type  int16
	Value []byte
}

type header struct {
	Op        byte
	SwitchMAC [macLen]byte
	HostMAC   [macLen]byte
	Seq       uint16
	ErrCode   int32
	Length    uint16
	// Fragment is non-zero when more packets of the same reply follow.
	Fragment uint16
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
	b[0], b[offOp] = protocolVersion, h.Op
	copy(b[offSwitchMAC:], h.SwitchMAC[:])
	copy(b[offHostMAC:], h.HostMAC[:])
	binary.BigEndian.PutUint16(b[offSeq:], h.Seq)
	binary.BigEndian.PutUint32(b[offErrCode:], uint32(h.ErrCode))
	binary.BigEndian.PutUint16(b[offLength:], uint16(headerLen+len(payload)))
	binary.BigEndian.PutUint16(b[offFragment:], h.Fragment)
	return append(b, payload...)
}

// looksLikeHeader reports whether a decrypted buffer plausibly is a packet.
func looksLikeHeader(b []byte) bool {
	return len(b) >= headerLen+len(packetEnd) && b[0] == protocolVersion && b[offOp] <= OpSetReply &&
		int(binary.BigEndian.Uint16(b[offLength:])) == len(b)
}

func unmarshal(b []byte) (header, []TLV, error) {
	var h header
	if !looksLikeHeader(b) {
		return h, nil, fmt.Errorf("escp: not a valid packet (%d bytes)", len(b))
	}
	h.Op = b[offOp]
	copy(h.SwitchMAC[:], b[offSwitchMAC:offSwitchMAC+macLen])
	copy(h.HostMAC[:], b[offHostMAC:offHostMAC+macLen])
	h.Seq = binary.BigEndian.Uint16(b[offSeq:])
	h.ErrCode = int32(binary.BigEndian.Uint32(b[offErrCode:]))
	h.Length = binary.BigEndian.Uint16(b[offLength:])
	h.Fragment = binary.BigEndian.Uint16(b[offFragment:])
	var tlvs []TLV
	p := b[headerLen:]
	for len(p) >= tlvHeaderLen {
		t := int16(binary.BigEndian.Uint16(p))
		l := int(binary.BigEndian.Uint16(p[2:]))
		if t == tlvEndType && l == 0 {
			break
		}
		if tlvHeaderLen+l > len(p) {
			return h, nil, fmt.Errorf("escp: truncated TLV type %d", t)
		}
		tlvs = append(tlvs, TLV{Type: t, Value: p[tlvHeaderLen : tlvHeaderLen+l]})
		p = p[tlvHeaderLen+l:]
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
