package wire

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

// Layout of the packet header (all fields big-endian).
const (
	protocolVersion = 1 // first header byte
	// HeaderLen is the total header size.
	HeaderLen = 32
	macLen    = 6 // length of a MAC address
	// TLVHeaderLen is the size of a TLV header: type (2 bytes) + length (2 bytes).
	TLVHeaderLen = 4

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

// Header is the 32-byte packet header.
type Header struct {
	Op        byte
	SwitchMAC [macLen]byte
	HostMAC   [macLen]byte
	Seq       uint16
	ErrCode   int32
	Length    uint16
	// Fragment is non-zero when more packets of the same reply follow.
	Fragment uint16
}

// Marshal encodes a header and TLVs into one packet, appending the terminating TLV and
// filling in the length field.
func Marshal(h Header, tlvs []TLV) []byte {
	payload := []byte{}
	for _, t := range tlvs {
		payload = binary.BigEndian.AppendUint16(payload, uint16(t.Type))
		payload = binary.BigEndian.AppendUint16(payload, uint16(len(t.Value)))
		payload = append(payload, t.Value...)
	}
	payload = append(payload, packetEnd...)
	b := make([]byte, HeaderLen, HeaderLen+len(payload))
	b[0], b[offOp] = protocolVersion, h.Op
	copy(b[offSwitchMAC:], h.SwitchMAC[:])
	copy(b[offHostMAC:], h.HostMAC[:])
	binary.BigEndian.PutUint16(b[offSeq:], h.Seq)
	binary.BigEndian.PutUint32(b[offErrCode:], uint32(h.ErrCode))
	binary.BigEndian.PutUint16(b[offLength:], uint16(HeaderLen+len(payload)))
	binary.BigEndian.PutUint16(b[offFragment:], h.Fragment)
	return append(b, payload...)
}

// LooksLikeHeader reports whether a decrypted buffer plausibly is a packet.
func LooksLikeHeader(b []byte) bool {
	return len(b) >= HeaderLen+len(packetEnd) && b[0] == protocolVersion && b[offOp] <= OpSetReply &&
		int(binary.BigEndian.Uint16(b[offLength:])) == len(b)
}

// Unmarshal decodes a packet. It validates the header and the TLV lengths and never
// reads beyond the buffer.
func Unmarshal(b []byte) (Header, []TLV, error) {
	var h Header
	if !LooksLikeHeader(b) {
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
	p := b[HeaderLen:]
	for len(p) >= TLVHeaderLen {
		t := int16(binary.BigEndian.Uint16(p))
		l := int(binary.BigEndian.Uint16(p[2:]))
		if t == tlvEndType && l == 0 {
			break
		}
		if TLVHeaderLen+l > len(p) {
			return h, nil, fmt.Errorf("escp: truncated TLV type %d", t)
		}
		tlvs = append(tlvs, TLV{Type: t, Value: p[TLVHeaderLen : TLVHeaderLen+l]})
		p = p[TLVHeaderLen+l:]
	}
	return h, tlvs, nil
}

// CString returns the bytes up to the first NUL (or all of them) as a string.
func CString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
