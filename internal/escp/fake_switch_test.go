package escp

import (
	"math/big"
	"net"
	"sync"
	"testing"
)

// fakeSwitch is a minimal in-process TL-SG108E speaking ESCP. It can decrypt
// the session key because the switch's 64-bit RSA modulus factors trivially
// (p=3443843009, q=3765816959), which is also why ESCP offers no real secrecy.
type fakeSwitch struct {
	t        *testing.T
	conn     *net.UDPConn
	user     string
	password string

	mu       sync.Mutex
	hostname string
	history  [][]TLV // every accepted SET, in order
	lastSet  []TLV   // settings TLVs (credentials stripped) of the most recent accepted SET
	port7    []byte
	setIP    []byte
	led      bool
	session  []byte
	gets     int
	sets     int
	dropNth  int // drop the nth received packet (1-based) to test retries
	recv     int
	mute     bool // swallow everything (switch gone)
}

var (
	fakeP = big.NewInt(3443843009)
	fakeQ = big.NewInt(3765816959)
)

func startFakeSwitch(t *testing.T) *fakeSwitch {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSwitch{t: t, conn: conn, user: "admin", password: "secret", led: true}
	t.Cleanup(func() { conn.Close() })
	go f.serve()
	return f
}

func (f *fakeSwitch) port() int { return f.conn.LocalAddr().(*net.UDPAddr).Port }

func (f *fakeSwitch) allSets() [][]TLV {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]TLV(nil), f.history...)
}

func (f *fakeSwitch) set() []TLV {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]TLV(nil), f.lastSet...)
}

func (f *fakeSwitch) state() (led bool, gets, sets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.led, f.gets, f.sets
}

func (f *fakeSwitch) decryptSessionKey(v []byte) []byte {
	words := new(big.Int)
	for i := 3; i >= 0; i-- {
		words.Lsh(words, 16)
		words.Or(words, big.NewInt(int64(v[2+2*i])|int64(v[3+2*i])<<8))
	}
	phi := new(big.Int).Mul(new(big.Int).Sub(fakeP, big.NewInt(1)), new(big.Int).Sub(fakeQ, big.NewInt(1)))
	d := new(big.Int).ModInverse(big.NewInt(rsaE), phi)
	m := new(big.Int).Exp(words, d, rsaN)
	return reverse(m.FillBytes(make([]byte, SessionKeyLen)))
}

func (f *fakeSwitch) serve() {
	buf := make([]byte, 4096)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		f.mu.Lock()
		f.recv++
		drop := f.mute || f.recv == f.dropNth
		f.mu.Unlock()
		if drop {
			continue
		}
		raw := append([]byte(nil), buf[:n]...)
		pkt := rc4Static(raw)
		if !looksLikeHeader(pkt) {
			f.mu.Lock()
			s := f.session
			f.mu.Unlock()
			if s == nil {
				continue
			}
			pkt = rc4Session(raw, s)
		}
		h, tlvs, err := unmarshal(pkt)
		if err != nil {
			continue
		}
		f.handle(from, h, tlvs)
	}
}

func (f *fakeSwitch) send(to *net.UDPAddr, op byte, req header, errCode int32, tlvs []TLV, useSession bool) {
	req.Op, req.ErrCode = op, errCode
	req.SwitchMAC = [6]byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}
	b := marshal(req, tlvs)
	f.mu.Lock()
	s := f.session
	f.mu.Unlock()
	if useSession && s != nil {
		b = rc4Session(b, s)
	} else {
		b = rc4Static(b)
	}
	_, _ = f.conn.WriteToUDP(b, to)
}

func (f *fakeSwitch) handle(from *net.UDPAddr, h header, tlvs []TLV) {
	find := func(t int16) ([]byte, bool) {
		for _, x := range tlvs {
			if x.Type == t {
				return x.Value, true
			}
		}
		return nil, false
	}
	switch h.Op {
	case OpDiscovery:
		f.send(from, OpReply, h, 0, []TLV{
			{TLVModel, []byte("TL-SG108E\x00")}, {TLVHostname, []byte("TL-SG108E-fake\x00")},
			{TLVMAC, []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}, {TLVIP, []byte{127, 0, 0, 1}},
			{TLVNetmask, []byte{255, 255, 255, 0}}, {TLVGateway, []byte{127, 0, 0, 254}},
			{TLVFirmware, []byte("1.0.0 Build 20230218 Rel.50633\x00")}, {TLVHardware, []byte("TL-SG108E 6.0\x00")},
			{TLVDHCP, []byte{1}}, {TLVFactoryDefault, []byte{1}},
		}, false)
	case OpSet:
		if v, ok := find(TLVRSAKey); ok {
			f.mu.Lock()
			f.session = f.decryptSessionKey(v)
			f.mu.Unlock()
			f.send(from, OpSetReply, h, ErrCodeKeyAccepted, nil, true)
			return
		}
		u, _ := find(TLVUsername)
		p, _ := find(TLVPassword)
		if cstring(u) != f.user || cstring(p) != f.password {
			f.send(from, OpSetReply, h, 5, nil, true)
			return
		}
		f.mu.Lock()
		f.lastSet = nil
		for _, t := range tlvs {
			if t.Type != TLVUsername && t.Type != TLVPassword {
				f.lastSet = append(f.lastSet, TLV{Type: t.Type, Value: append([]byte(nil), t.Value...)})
			}
		}
		f.history = append(f.history, append([]TLV(nil), f.lastSet...))
		f.mu.Unlock()
		if v, ok := find(TLVPorts); ok && len(v) == 7 && v[0] == 7 {
			f.mu.Lock()
			f.port7 = append([]byte(nil), v...)
			f.sets++
			f.mu.Unlock()
		}
		if v, ok := find(TLVHostname); ok {
			f.mu.Lock()
			f.hostname = cstring(v)
			f.setIP, _ = find(TLVIP)
			f.sets++
			f.mu.Unlock()
		}
		if v, ok := find(TLVLED); ok && len(v) == 1 {
			f.mu.Lock()
			f.led = v[0] == 1
			f.sets++
			f.mu.Unlock()
		}
		f.send(from, OpSetReply, h, 0, nil, true)
	case OpGet:
		if _, ok := find(TLVQoSMode); ok {
			f.send(from, OpReply, h, 0, []TLV{{TLVQoSMode, []byte{0}}}, true)
			return
		}
		if _, ok := find(TLVQoSPriority); ok {
			var rows []TLV
			for p := 1; p <= 8; p++ {
				rows = append(rows, TLV{TLVQoSPriority, []byte{byte(p), 0}})
			}
			f.send(from, OpReply, h, 0, rows, true)
			return
		}
		if _, ok := find(TLVLED); ok {
			f.mu.Lock()
			f.gets++
			v := byte(0)
			if f.led {
				v = 1
			}
			f.mu.Unlock()
			f.send(from, OpReply, h, 0, []TLV{{TLVLED, []byte{v}}}, true)
		}
	}
}
