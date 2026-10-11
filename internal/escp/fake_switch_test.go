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

	backupMode string // config backup misbehaviour: "", "badmagic", "short", "dropsecond", "dropfirst", "silent", "flood"

	// Programmable misbehaviour, set through configure().
	getFn          func(types []int16) (tlvs []TLV, code int32, drop bool) // scripted GET replies
	rsaDrop        bool                                                    // never answer the key exchange
	rsaCode        int32                                                   // answer the key exchange with this code instead of ErrCodeKeyAccepted
	dropSets       bool                                                    // never answer authenticated SETs
	discoverTLVs   []TLV                                                   // replace the discovery reply (nil: the default TL-SG108E reply)
	rejectSetsFrom int                                                     // reject the nth and later authenticated SETs (1-based)
}

var (
	fakeP = big.NewInt(3443843009)
	fakeQ = big.NewInt(3765816959)
)

// freeUDPPort returns a currently unused UDP port so tests never bind the
// protocol's real client port 29809.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// newBoundClient dials a client on a free local port and binds its socket
// immediately, retrying with another port if something grabbed the first one
// in the meantime. Once it returns, the port is held by the client, so two
// tests can never end up on the same port.
func newBoundClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		cfg.HostPort = freeUDPPort(t)
		c, err := Dial(cfg)
		if err != nil {
			t.Fatal(err)
		}
		c.mu.Lock()
		lastErr = c.open()
		c.mu.Unlock()
		if lastErr == nil {
			return c
		}
	}
	t.Fatalf("no free UDP port after 20 attempts: %v", lastErr)
	return nil
}

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

func (f *fakeSwitch) dropPacket(n int) { f.mu.Lock(); f.dropNth = n; f.mu.Unlock() }
func (f *fakeSwitch) goMute()          { f.mu.Lock(); f.mute = true; f.mu.Unlock() }
func (f *fakeSwitch) clearSession()    { f.mu.Lock(); f.session = nil; f.mu.Unlock() }

// configure changes the fake's behaviour under its lock.
func (f *fakeSwitch) configure(fn func(f *fakeSwitch)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

func (f *fakeSwitch) port() int { return f.conn.LocalAddr().(*net.UDPAddr).Port }

func (f *fakeSwitch) setBackupMode(mode string) {
	f.mu.Lock()
	f.backupMode = mode
	f.mu.Unlock()
}

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

// fakeConfig is a 2258-byte stand-in for the switch's configuration file.
func fakeConfig() []byte {
	b := make([]byte, 2258)
	copy(b, ConfigMagic)
	for i := 4; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	return b
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
		f.mu.Lock()
		override := f.discoverTLVs
		f.mu.Unlock()
		if override != nil {
			f.send(from, OpReply, h, 0, override, false)
			return
		}
		f.send(from, OpReply, h, 0, []TLV{
			{TLVModel, []byte("TL-SG108E\x00")}, {TLVHostname, []byte("TL-SG108E-fake\x00")},
			{TLVMAC, []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}, {TLVIP, []byte{127, 0, 0, 1}},
			{TLVNetmask, []byte{255, 255, 255, 0}}, {TLVGateway, []byte{127, 0, 0, 254}},
			{TLVFirmware, []byte("1.0.0 Build 20230218 Rel.50633\x00")}, {TLVHardware, []byte("TL-SG108E 6.0\x00")},
			{TLVDHCP, []byte{1}}, {TLVFactoryDefault, []byte{1}},
		}, false)
	case OpSet:
		if _, ok := find(TLVConfigFile); ok && len(tlvs) == 3 {
			u, _ := find(TLVUsername)
			p, _ := find(TLVPassword)
			if cstring(u) != f.user || cstring(p) != f.password {
				f.send(from, OpSetReply, h, 5, nil, true)
				return
			}
			f.mu.Lock()
			mode := f.backupMode
			f.mu.Unlock()
			if mode == "silent" {
				return
			}
			if mode == "flood" {
				// A sender that never clears the fragment flag.
				h1 := h
				h1.Fragment = 1
				for i := 0; i < 100; i++ {
					f.send(from, OpSetReply, h1, 0, []TLV{{TLVConfigFile, make([]byte, 1000)}}, true)
				}
				return
			}
			cfg := fakeConfig()
			switch mode {
			case "badmagic":
				cfg[0] = 0
			case "short":
				cfg = cfg[:4]
			}
			first, second := cfg, []byte(nil)
			if len(cfg) > 1400 {
				first, second = cfg[:1400], cfg[1400:]
			}
			h1 := h
			h1.Fragment = 1
			if second == nil {
				h1.Fragment = 0
			}
			if mode != "dropfirst" {
				f.send(from, OpSetReply, h1, 0, []TLV{{TLVConfigFile, first}}, true)
			}
			if second != nil && mode != "dropsecond" {
				f.send(from, OpSetReply, h, 0, []TLV{{TLVConfigFile, second}}, true)
			}
			return
		}
		if v, ok := find(TLVRSAKey); ok {
			f.mu.Lock()
			drop, code := f.rsaDrop, f.rsaCode
			if !drop {
				f.session = f.decryptSessionKey(v)
			}
			f.mu.Unlock()
			if drop {
				return
			}
			if code == 0 {
				code = ErrCodeKeyAccepted
			}
			f.send(from, OpSetReply, h, code, nil, true)
			return
		}
		f.mu.Lock()
		dropSets, rejectFrom, accepted := f.dropSets, f.rejectSetsFrom, len(f.history)
		f.mu.Unlock()
		if dropSets {
			return
		}
		u, _ := find(TLVUsername)
		p, _ := find(TLVPassword)
		if cstring(u) != f.user || cstring(p) != f.password {
			f.send(from, OpSetReply, h, 5, nil, true)
			return
		}
		if rejectFrom > 0 && accepted+1 >= rejectFrom {
			f.send(from, OpSetReply, h, 9, nil, true)
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
		f.mu.Lock()
		getFn := f.getFn
		f.mu.Unlock()
		if getFn != nil {
			types := make([]int16, len(tlvs))
			for i, t := range tlvs {
				types[i] = t.Type
			}
			reply, code, drop := getFn(types)
			if !drop {
				f.send(from, OpReply, h, code, reply, true)
			}
			return
		}
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
