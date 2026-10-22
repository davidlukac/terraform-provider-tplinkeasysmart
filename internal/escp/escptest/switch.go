package escptest

import (
	"math/big"
	"net"
	"sync"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Credentials the switch accepts.
const (
	Username = "admin"
	Password = "secret"
)

// DefaultFirmware is the firmware version the switch reports until SetFirmware changes it:
// the build the client and the provider were verified against.
const DefaultFirmware = "1.0.0 Build 20230218 Rel.50633"

// BackupMode selects how the switch misbehaves when the configuration is read.
type BackupMode string

// The ways a configuration backup can go wrong; BackupOK is the well-behaved switch.
const (
	BackupOK         BackupMode = ""
	BackupBadMagic   BackupMode = "badmagic"   // the file does not start with the configuration magic
	BackupShort      BackupMode = "short"      // only the magic, no body
	BackupDropSecond BackupMode = "dropsecond" // the last fragment never arrives
	BackupDropFirst  BackupMode = "dropfirst"  // only the last fragment arrives
	BackupSilent     BackupMode = "silent"     // no reply at all
	BackupFlood      BackupMode = "flood"      // fragments that never end
)

// Switch is the fake. Create it with Start; change its behaviour with Configure.
// It can decrypt the session key because the switch's 64-bit RSA modulus factors
// trivially (p=3443843009, q=3765816959), which is also why ESCP offers no real secrecy.
type Switch struct {
	conn     *net.UDPConn
	user     string
	password string

	mu               sync.Mutex
	hostname         string
	history          [][]wire.TLV // every accepted SET, in order
	lastSet          []wire.TLV   // settings TLVs (credentials stripped) of the most recent accepted SET
	port7            []byte
	setIP            []byte
	staleDiscoveries int              // the next n discoveries still report 127.0.0.9, like a network whose caches have not caught up
	addr             map[int16][]byte // IP, mask and gateway as last set; discovery reports them instead of the defaults
	led              bool
	firmware         string
	session          []byte
	gets             int
	sets             int
	dropNth          int // drop the nth received packet (1-based) to test retries
	recv             int
	mute             bool // swallow everything (switch gone)
	backupMode       BackupMode

	// Programmable misbehaviour. Set these inside Configure, which holds the lock.

	// GetFn scripts the reply to GETs: the TLVs, the error code, and whether to stay silent.
	GetFn func(types []int16) (tlvs []wire.TLV, code int32, drop bool)
	// RSADrop makes the switch ignore the key exchange.
	RSADrop bool
	// RSACode answers the key exchange with this code instead of the success code.
	RSACode int32
	// DropSets makes the switch ignore authenticated SETs.
	DropSets bool
	// DiscoverTLVs replaces the discovery reply; nil keeps the default TL-SG108E reply.
	DiscoverTLVs []wire.TLV
	// RejectSetsFrom rejects the nth and every later authenticated SET (1-based); 0 accepts all.
	RejectSetsFrom int
	// RejectCode is the reply code of a rejected SET; 0 means 9.
	RejectCode int32
	// StaleAfterSet makes the next n discoveries after a system SET still report the old address.
	StaleAfterSet int
}

var (
	fakeP = big.NewInt(3443843009)
	fakeQ = big.NewInt(3765816959)
)

// FreeUDPPort returns a currently unused UDP port so tests never bind the
// protocol's real client port 29809.
func FreeUDPPort(t testing.TB) int {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }() // best effort: only the port number is kept
	return c.LocalAddr().(*net.UDPAddr).Port
}

// Start runs a fake switch on a free loopback UDP port until the test ends. The LED starts on.
func Start(t testing.TB) *Switch {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &Switch{conn: conn, user: Username, password: Password, led: true, firmware: DefaultFirmware}
	t.Cleanup(func() { _ = conn.Close() }) // best effort at the end of the test
	go f.serve()
	return f
}

// Port is the UDP port the switch listens on; give it to the client as its switch port.
func (f *Switch) Port() int { return f.conn.LocalAddr().(*net.UDPAddr).Port }

// DropPacket makes the switch ignore the nth packet it receives (1-based), to test retries.
func (f *Switch) DropPacket(n int) { f.mu.Lock(); f.dropNth = n; f.mu.Unlock() }

// Mute makes the switch ignore everything from now on, as if it had gone away.
func (f *Switch) Mute() { f.mu.Lock(); f.mute = true; f.mu.Unlock() }

// ClearSession forgets the session key, as after a reboot: the client must exchange keys again.
func (f *Switch) ClearSession() { f.mu.Lock(); f.session = nil; f.mu.Unlock() }

// Configure changes the fake's behaviour under its lock.
func (f *Switch) Configure(fn func(f *Switch)) { f.mu.Lock(); fn(f); f.mu.Unlock() }

// SetBackupMode chooses how the configuration backup misbehaves.
func (f *Switch) SetBackupMode(mode BackupMode) {
	f.mu.Lock()
	f.backupMode = mode
	f.mu.Unlock()
}

// AllSets returns the settings of every accepted SET in order, credentials removed.
func (f *Switch) AllSets() [][]wire.TLV {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]wire.TLV(nil), f.history...)
}

// LastSet returns the settings of the most recent accepted SET, credentials removed.
func (f *Switch) LastSet() []wire.TLV {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]wire.TLV(nil), f.lastSet...)
}

// State returns the LED state and how many LED/hostname/port-7 writes and LED reads
// the switch has handled (sets counts writes of those three settings only).
func (f *Switch) State() (led bool, gets, sets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.led, f.gets, f.sets
}

// SetFirmware changes the firmware version the switch reports in discovery replies.
func (f *Switch) SetFirmware(version string) {
	f.mu.Lock()
	f.firmware = version
	f.mu.Unlock()
}

// Hostname is the device description set by the last hostname SET ("" before any).
func (f *Switch) Hostname() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hostname
}

// IPSent is the IP address carried by the last hostname SET (nil before any).
func (f *Switch) IPSent() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.setIP...)
}

// Port7Setting is the value of the last port SET addressed to port 7 (nil before any).
func (f *Switch) Port7Setting() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.port7...)
}

// DecryptSessionKey recovers the session key from the RSA key-exchange value a client sent.
func DecryptSessionKey(v []byte) []byte {
	words := new(big.Int)
	for i := 3; i >= 0; i-- {
		words.Lsh(words, 16)
		words.Or(words, big.NewInt(int64(v[2+2*i])|int64(v[3+2*i])<<8))
	}
	phi := new(big.Int).Mul(new(big.Int).Sub(fakeP, big.NewInt(1)), new(big.Int).Sub(fakeQ, big.NewInt(1)))
	d := new(big.Int).ModInverse(big.NewInt(wire.RSAExponent), phi)
	m := new(big.Int).Exp(words, d, wire.RSAModulus())
	return wire.ReverseBytes(m.FillBytes(make([]byte, wire.SessionKeyLen)))
}

func (f *Switch) serve() {
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
		pkt := wire.RC4Static(raw)
		if !wire.LooksLikeHeader(pkt) {
			f.mu.Lock()
			s := f.session
			f.mu.Unlock()
			if s == nil {
				continue
			}
			pkt = wire.RC4Session(raw, s)
		}
		h, tlvs, err := wire.Unmarshal(pkt)
		if err != nil {
			continue
		}
		f.handle(from, h, tlvs)
	}
}

// ConfigFile is a 2258-byte stand-in for the switch's configuration file.
func ConfigFile() []byte {
	b := make([]byte, 2258)
	copy(b, wire.ConfigMagic)
	for i := 4; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	return b
}

func (f *Switch) send(to *net.UDPAddr, op byte, req wire.Header, errCode int32, tlvs []wire.TLV, useSession bool) {
	req.Op, req.ErrCode = op, errCode
	req.SwitchMAC = [6]byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}
	b := wire.Marshal(req, tlvs)
	f.mu.Lock()
	s := f.session
	f.mu.Unlock()
	if useSession && s != nil {
		b = wire.RC4Session(b, s)
	} else {
		b = wire.RC4Static(b)
	}
	_, _ = f.conn.WriteToUDP(b, to)
}

func (f *Switch) handle(from *net.UDPAddr, h wire.Header, tlvs []wire.TLV) {
	find := func(t int16) ([]byte, bool) {
		for _, x := range tlvs {
			if x.Type == t {
				return x.Value, true
			}
		}
		return nil, false
	}
	switch h.Op {
	case wire.OpDiscovery:
		f.mu.Lock()
		override := f.DiscoverTLVs
		f.mu.Unlock()
		if override != nil {
			f.send(from, wire.OpReply, h, 0, override, false)
			return
		}
		f.mu.Lock()
		addr, firmware := f.addr, f.firmware
		if f.staleDiscoveries > 0 {
			f.staleDiscoveries--
			addr = map[int16][]byte{wire.TLVIP: {127, 0, 0, 9}, wire.TLVNetmask: {255, 255, 255, 0}, wire.TLVGateway: {127, 0, 0, 254}}
		}
		f.mu.Unlock()
		ip, mask, gw := []byte{127, 0, 0, 1}, []byte{255, 255, 255, 0}, []byte{127, 0, 0, 254}
		if addr != nil {
			ip, mask, gw = addr[wire.TLVIP], addr[wire.TLVNetmask], addr[wire.TLVGateway]
		}
		f.send(from, wire.OpReply, h, 0, []wire.TLV{
			{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVHostname, Value: []byte("TL-SG108E-fake\x00")},
			{Type: wire.TLVMAC, Value: []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}, {Type: wire.TLVIP, Value: ip},
			{Type: wire.TLVNetmask, Value: mask}, {Type: wire.TLVGateway, Value: gw},
			{Type: wire.TLVFirmware, Value: []byte(firmware + "\x00")}, {Type: wire.TLVHardware, Value: []byte("TL-SG108E 6.0\x00")},
			{Type: wire.TLVDHCP, Value: []byte{1}}, {Type: wire.TLVFactoryDefault, Value: []byte{1}},
		}, false)
	case wire.OpSet:
		if _, ok := find(wire.TLVConfigFile); ok && len(tlvs) == 3 {
			u, _ := find(wire.TLVUsername)
			p, _ := find(wire.TLVPassword)
			if wire.CString(u) != f.user || wire.CString(p) != f.password {
				f.send(from, wire.OpSetReply, h, 5, nil, true)
				return
			}
			f.mu.Lock()
			mode := f.backupMode
			f.mu.Unlock()
			if mode == BackupSilent {
				return
			}
			if mode == BackupFlood {
				// A sender that never clears the fragment flag.
				h1 := h
				h1.Fragment = 1
				for i := 0; i < 100; i++ {
					f.send(from, wire.OpSetReply, h1, 0, []wire.TLV{{Type: wire.TLVConfigFile, Value: make([]byte, 1000)}}, true)
				}
				return
			}
			cfg := ConfigFile()
			switch mode {
			case BackupBadMagic:
				cfg[0] = 0
			case BackupShort:
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
			if mode != BackupDropFirst {
				f.send(from, wire.OpSetReply, h1, 0, []wire.TLV{{Type: wire.TLVConfigFile, Value: first}}, true)
			}
			if second != nil && mode != BackupDropSecond {
				f.send(from, wire.OpSetReply, h, 0, []wire.TLV{{Type: wire.TLVConfigFile, Value: second}}, true)
			}
			return
		}
		if v, ok := find(wire.TLVRSAKey); ok {
			f.mu.Lock()
			drop, code := f.RSADrop, f.RSACode
			if !drop {
				f.session = DecryptSessionKey(v)
			}
			f.mu.Unlock()
			if drop {
				return
			}
			if code == 0 {
				code = wire.ErrCodeKeyAccepted
			}
			f.send(from, wire.OpSetReply, h, code, nil, true)
			return
		}
		f.mu.Lock()
		DropSets, rejectFrom, RejectCode, accepted := f.DropSets, f.RejectSetsFrom, f.RejectCode, len(f.history)
		f.mu.Unlock()
		if DropSets {
			return
		}
		u, _ := find(wire.TLVUsername)
		p, _ := find(wire.TLVPassword)
		if wire.CString(u) != f.user || wire.CString(p) != f.password {
			f.send(from, wire.OpSetReply, h, 5, nil, true)
			return
		}
		if rejectFrom > 0 && accepted+1 >= rejectFrom {
			if RejectCode == 0 {
				RejectCode = 9
			}
			f.send(from, wire.OpSetReply, h, RejectCode, nil, true)
			return
		}
		f.mu.Lock()
		f.lastSet = nil
		for _, t := range tlvs {
			if t.Type != wire.TLVUsername && t.Type != wire.TLVPassword {
				f.lastSet = append(f.lastSet, wire.TLV{Type: t.Type, Value: append([]byte(nil), t.Value...)})
			}
		}
		f.history = append(f.history, append([]wire.TLV(nil), f.lastSet...))
		f.mu.Unlock()
		if v, ok := find(wire.TLVPorts); ok && len(v) == 7 && v[0] == 7 {
			f.mu.Lock()
			f.port7 = append([]byte(nil), v...)
			f.sets++
			f.mu.Unlock()
		}
		if v, ok := find(wire.TLVHostname); ok {
			f.mu.Lock()
			f.hostname = wire.CString(v)
			f.setIP, _ = find(wire.TLVIP)
			f.staleDiscoveries = f.StaleAfterSet
			if ip, ok := find(wire.TLVIP); ok {
				mask, _ := find(wire.TLVNetmask)
				gw, _ := find(wire.TLVGateway)
				f.addr = map[int16][]byte{wire.TLVIP: ip, wire.TLVNetmask: mask, wire.TLVGateway: gw}
			}
			f.sets++
			f.mu.Unlock()
		}
		if v, ok := find(wire.TLVLED); ok && len(v) == 1 {
			f.mu.Lock()
			f.led = v[0] == 1
			f.sets++
			f.mu.Unlock()
		}
		f.send(from, wire.OpSetReply, h, 0, nil, true)
	case wire.OpGet:
		f.mu.Lock()
		GetFn := f.GetFn
		f.mu.Unlock()
		if GetFn != nil {
			types := make([]int16, len(tlvs))
			for i, t := range tlvs {
				types[i] = t.Type
			}
			reply, code, drop := GetFn(types)
			if !drop {
				f.send(from, wire.OpReply, h, code, reply, true)
			}
			return
		}
		if _, ok := find(wire.TLVQoSMode); ok {
			f.send(from, wire.OpReply, h, 0, []wire.TLV{{Type: wire.TLVQoSMode, Value: []byte{0}}}, true)
			return
		}
		if _, ok := find(wire.TLVQoSPriority); ok {
			var rows []wire.TLV
			for p := 1; p <= 8; p++ {
				rows = append(rows, wire.TLV{Type: wire.TLVQoSPriority, Value: []byte{byte(p), 0}})
			}
			f.send(from, wire.OpReply, h, 0, rows, true)
			return
		}
		if _, ok := find(wire.TLVPortCount); ok {
			f.send(from, wire.OpReply, h, 0, []wire.TLV{{Type: wire.TLVPortCount, Value: []byte{8}}}, true)
			return
		}
		if _, ok := find(wire.TLVAutoSave); ok {
			f.send(from, wire.OpReply, h, 0, []wire.TLV{{Type: wire.TLVAutoSave, Value: []byte{1}}}, true)
			return
		}
		if _, ok := find(wire.TLVLED); ok {
			f.mu.Lock()
			f.gets++
			v := byte(0)
			if f.led {
				v = 1
			}
			f.mu.Unlock()
			f.send(from, wire.OpReply, h, 0, []wire.TLV{{Type: wire.TLVLED, Value: []byte{v}}}, true)
		}
	}
}
