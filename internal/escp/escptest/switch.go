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

// SystemSetBehaviour is what the switch does after it applies a system SET, the write that
// carries the hostname and the management address.
type SystemSetBehaviour int

// The behaviours after a system SET. SystemSetReplies is the well-behaved switch.
const (
	// SystemSetReplies answers the SET and carries on.
	SystemSetReplies SystemSetBehaviour = iota
	// SystemSetLeaves applies the SET without answering it, then ignores every packet except
	// discovery, which it still answers: a switch that has moved to its new address, seen from
	// the old one. The fake cannot change its source address, so discovery still reports the
	// new settings from the same address.
	SystemSetLeaves
	// SystemSetVanishes applies the SET without answering it, then ignores everything,
	// discovery included: a switch that moved and cannot be found.
	SystemSetVanishes
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
	dropFirst        int // drop the first n received packets
	droppedSets      int // authenticated SETs ignored under DropSets
	recv             int
	mute             bool // swallow everything (switch gone)
	backupMode       BackupMode
	departed         bool               // a system SET was applied under OnSystemSet and the switch has left
	stateful         bool               // EnableState was called: SETs are remembered and answered by later GETs
	store            map[int16][]byte   // the last value of each settings TLV written (or seeded) under EnableState
	rows             map[int16][][]byte // tables with one TLV per row (ports, priorities, rates), keyed by the first byte of a row

	// Programmable misbehaviour. Set these inside Configure, which holds the lock.

	// GetFn scripts the reply to GETs: the settings, the error code, and whether to stay silent.
	GetFn func(types []int16) (settings []Setting, code int32, drop bool)
	// RSADrop makes the switch ignore the key exchange.
	RSADrop bool
	// RSACode answers the key exchange with this code instead of the success code.
	RSACode int32
	// DropSets makes the switch ignore authenticated SETs.
	DropSets bool
	// DiscoverReply replaces the discovery reply; nil keeps the default TL-SG108E reply.
	DiscoverReply []Setting
	// RejectSetsFrom rejects the nth and every later authenticated SET (1-based); 0 accepts all.
	RejectSetsFrom int
	// RejectCode is the reply code of a rejected SET; 0 means 9.
	RejectCode int32
	// StaleAfterSet makes the next n discoveries after a system SET still report the old address.
	StaleAfterSet int
	// OnSystemSet is what the switch does after applying a system SET; the default replies.
	OnSystemSet SystemSetBehaviour
	// AfterSystemSet, when set, is called (without the lock) once a system SET has been applied
	// and before the switch replies or leaves, so a test can act at exactly that moment.
	AfterSystemSet func()
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

// SetsDropped is the number of authenticated SET datagrams the switch ignored under DropSets. A
// test of "the write was lost" uses it to show that the write really was sent, so that it does
// not pass for the wrong reason (WriteCount is zero for a write that never left the client too).
func (f *Switch) SetsDropped() int { f.mu.Lock(); defer f.mu.Unlock(); return f.droppedSets }

// Received is the number of datagrams that have reached the switch so far, whatever it did with
// them. A discovery probe is two datagrams (see DropFirst). Datagrams on one socket arrive in
// order, so a count read after a reply to a later request includes every earlier one.
func (f *Switch) Received() int { f.mu.Lock(); defer f.mu.Unlock(); return f.recv }

// EnableState makes the switch remember settings: every accepted SET stores the value of each
// TLV it carries, and a GET for a type it holds (seeded here or written since) answers with that
// value, so a write can be read back the way the real switch does it. Types it holds nothing
// for fall through to the scripted replies (LED, QoS, port count), and GetFn still wins. The
// seeds are the settings the switch starts with.
func (f *Switch) EnableState(seed ...Setting) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stateful = true
	f.store = map[int16][]byte{}
	f.rows = map[int16][][]byte{}
	for _, s := range seed {
		f.store[s.Type] = append([]byte(nil), s.Value...)
	}
}

// DropFirst makes the switch ignore the first n packets it receives. A discovery probe is sent
// twice per attempt (to the switch and to the broadcast address, which a test points at the same
// fake), so DropFirst(2) loses exactly one whole attempt of a probe.
func (f *Switch) DropFirst(n int) { f.mu.Lock(); f.dropFirst = n; f.mu.Unlock() }

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

// Setting is one entry of a request or a reply: a protocol type number (the numbers of
// internal/escp/PROTOCOL.md) and its value. It is the fake's own type, so a test outside the
// escp module needs nothing from the client's internals to read or script traffic.
type Setting struct {
	Type  int16
	Value []byte
}

func toTLVs(in []Setting) []wire.TLV {
	out := make([]wire.TLV, len(in))
	for i, s := range in {
		out[i] = wire.TLV{Type: s.Type, Value: s.Value}
	}
	return out
}

func toSettings(in []wire.TLV) []Setting {
	out := make([]Setting, len(in))
	for i, t := range in {
		out[i] = Setting{Type: t.Type, Value: append([]byte(nil), t.Value...)}
	}
	return out
}

// WriteCount is the number of SETs the switch accepted: the answer to "was anything written?".
func (f *Switch) WriteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.history)
}

// AllSets returns the settings of every accepted SET in order, credentials removed.
func (f *Switch) AllSets() [][]Setting {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]Setting, len(f.history))
	for i, h := range f.history {
		out[i] = toSettings(h)
	}
	return out
}

// LastSet returns the settings of the most recent accepted SET, credentials removed.
func (f *Switch) LastSet() []Setting {
	f.mu.Lock()
	defer f.mu.Unlock()
	return toSettings(f.lastSet)
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
// It returns nil for a value too short to be one.
func DecryptSessionKey(v []byte) []byte {
	if len(v) < 2+2*4 {
		return nil
	}
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
		drop := f.mute || f.recv == f.dropNth || f.recv <= f.dropFirst
		f.mu.Unlock()
		if drop {
			continue
		}
		raw := append([]byte(nil), buf[:n]...)
		pkt := wire.RC4Static(raw)
		viaSession := false
		if !wire.LooksLikeHeader(pkt) {
			f.mu.Lock()
			s := f.session
			f.mu.Unlock()
			if s == nil {
				continue
			}
			pkt = wire.RC4Session(raw, s)
			viaSession = true
		}
		h, tlvs, err := wire.Unmarshal(pkt)
		if err != nil {
			continue
		}
		f.handle(from, h, tlvs, viaSession)
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

// handle answers one packet. viaSession says it was encrypted with the session key; credentials
// must never travel under the static key, so a SET that carries them that way is ignored, as the
// vendor utility never sends one and a client that did would be sending the password in the clear.
func (f *Switch) handle(from *net.UDPAddr, h wire.Header, tlvs []wire.TLV, viaSession bool) {
	f.mu.Lock()
	gone := f.departed && (h.Op != wire.OpDiscovery || f.OnSystemSet == SystemSetVanishes)
	f.mu.Unlock()
	if gone {
		return
	}
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
		override := f.DiscoverReply
		f.mu.Unlock()
		if override != nil {
			f.send(from, wire.OpReply, h, 0, toTLVs(override), false)
			return
		}
		f.mu.Lock()
		addr, firmware := f.addr, f.firmware
		name := "TL-SG108E-fake"
		if f.hostname != "" {
			name = f.hostname
		}
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
			{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVHostname, Value: []byte(name + "\x00")},
			{Type: wire.TLVMAC, Value: []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}, {Type: wire.TLVIP, Value: ip},
			{Type: wire.TLVNetmask, Value: mask}, {Type: wire.TLVGateway, Value: gw},
			{Type: wire.TLVFirmware, Value: []byte(firmware + "\x00")}, {Type: wire.TLVHardware, Value: []byte("TL-SG108E 6.0\x00")},
			{Type: wire.TLVDHCP, Value: []byte{1}}, {Type: wire.TLVFactoryDefault, Value: []byte{1}},
		}, false)
	case wire.OpSet:
		if _, hasUser := find(wire.TLVUsername); hasUser && !viaSession {
			return
		}
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
			if key := DecryptSessionKey(v); !drop && key != nil {
				f.session = key
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
		if DropSets {
			f.droppedSets++
		}
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
		if f.stateful {
			for _, t := range f.lastSet {
				f.storeSet(t)
			}
		}
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
		if _, isSystem := find(wire.TLVHostname); isSystem {
			f.mu.Lock()
			leaves := f.OnSystemSet != SystemSetReplies
			f.departed = f.departed || leaves
			applied := f.AfterSystemSet
			f.mu.Unlock()
			if applied != nil {
				applied()
			}
			if leaves {
				return
			}
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
				f.send(from, wire.OpReply, h, code, toTLVs(reply), true)
			}
			return
		}
		if held := f.held(tlvs); len(held) > 0 {
			f.send(from, wire.OpReply, h, 0, held, true)
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

// EnableRows declares a table the switch keeps one TLV per row of (the ports, the QoS priorities,
// the bandwidth and storm rates) and seeds it. A row is identified by its first byte, the port
// number: a SET replaces the row with the same first byte, a GET returns every row in order.
// A write of the port table (TLV 4096) keeps what only the switch decides, the LAG and the
// actual speed, and derives the actual flow control from the configured one. Call EnableState
// first.
func (f *Switch) EnableRows(typ int16, rows ...[]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[typ] = nil
	for _, r := range rows {
		f.rows[typ] = append(f.rows[typ], append([]byte(nil), r...))
	}
}

// storeSet remembers one written TLV: into its table when it has one, else as a single value.
func (f *Switch) storeSet(t wire.TLV) {
	table, ok := f.rows[t.Type]
	if !ok {
		f.store[t.Type] = append([]byte(nil), t.Value...)
		return
	}
	row := append([]byte(nil), t.Value...)
	for i, old := range table {
		if old[0] != row[0] {
			continue
		}
		if t.Type == wire.TLVPorts && len(row) == len(old) {
			row[portRowLAG], row[portRowActualSpeed] = old[portRowLAG], old[portRowActualSpeed]
			row[portRowActualFlow] = 0
			if row[portRowConfiguredFlow] == 1 && old[portRowActualSpeed] != 0 {
				row[portRowActualFlow] = 1
			}
		}
		table[i] = row
		return
	}
	f.rows[t.Type] = append(table, row)
}

// Offsets in a 7-byte port row (TLV 4096).
const (
	portRowLAG            = 2
	portRowActualSpeed    = 4
	portRowConfiguredFlow = 5
	portRowActualFlow     = 6
)

// held returns the stored settings for the requested types, or nil when the switch is not
// stateful or holds none of them.
func (f *Switch) held(req []wire.TLV) []wire.TLV {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.stateful {
		return nil
	}
	var out []wire.TLV
	for _, r := range req {
		// The real switch answers a request for the IGMP state with report suppression as well.
		if r.Type == wire.TLVIGMPEnabled {
			if v, ok := f.store[wire.TLVIGMPSuppression]; ok {
				out = append(out, wire.TLV{Type: wire.TLVIGMPSuppression, Value: append([]byte(nil), v...)})
			}
		}
		if table, ok := f.rows[r.Type]; ok {
			for _, row := range table {
				out = append(out, wire.TLV{Type: r.Type, Value: append([]byte(nil), row...)})
			}
			continue
		}
		if v, ok := f.store[r.Type]; ok {
			out = append(out, wire.TLV{Type: r.Type, Value: append([]byte(nil), v...)})
		}
	}
	return out
}
