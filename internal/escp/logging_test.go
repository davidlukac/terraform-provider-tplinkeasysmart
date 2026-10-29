package escp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// logLine is one message the library logged, with its attributes by name.
type logLine struct {
	level slog.Level
	msg   string
	attrs map[string]any
	dupes []string // attribute names that arrived more than once
}

// logRecorder is a slog handler that keeps every line, for tests that assert on what the library says.
type logRecorder struct {
	mu    *sync.Mutex
	lines *[]logLine
	with  map[string]any
	dupes []string // names given to With more than once
}

func newLogRecorder() (*slog.Logger, *logRecorder) {
	r := &logRecorder{mu: &sync.Mutex{}, lines: &[]logLine{}, with: map[string]any{}}
	return slog.New(r), r
}

func (r *logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	attrs := map[string]any{}
	var dupes []string
	for k, v := range r.with {
		attrs[k] = v
	}
	rec.Attrs(func(a slog.Attr) bool {
		if _, again := attrs[a.Key]; again {
			dupes = append(dupes, a.Key)
		}
		attrs[a.Key] = a.Value.Any()
		return true
	})
	r.mu.Lock()
	*r.lines = append(*r.lines, logLine{level: rec.Level, msg: rec.Message, attrs: attrs, dupes: append(dupes, r.dupes...)})
	r.mu.Unlock()
	return nil
}
func (r *logRecorder) WithAttrs(as []slog.Attr) slog.Handler {
	with := map[string]any{}
	for k, v := range r.with {
		with[k] = v
	}
	dupes := append([]string(nil), r.dupes...)
	for _, a := range as {
		if _, again := with[a.Key]; again {
			dupes = append(dupes, a.Key)
		}
		with[a.Key] = a.Value.Any()
	}
	return &logRecorder{mu: r.mu, lines: r.lines, with: with, dupes: dupes}
}
func (r *logRecorder) WithGroup(string) slog.Handler { return r }

// assertIdentified fails unless there is at least one line and every line carries component=escp
// exactly once (and no other attribute twice): the identification a consumer filters on.
func (r *logRecorder) assertIdentified(t *testing.T) {
	t.Helper()
	lines := r.all()
	if len(lines) == 0 {
		t.Fatal("the library logged nothing")
	}
	for _, l := range lines {
		if l.attrs[componentKey] != componentName {
			t.Errorf("line %q has component=%v, want %s", l.msg, l.attrs[componentKey], componentName)
		}
		if len(l.dupes) > 0 {
			t.Errorf("line %q repeats the attributes %v", l.msg, l.dupes)
		}
	}
}

// all returns a copy of the lines logged so far.
func (r *logRecorder) all() []logLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]logLine(nil), *r.lines...)
}

// named returns the lines with the given message.
func (r *logRecorder) named(msg string) []logLine {
	var out []logLine
	for _, l := range r.all() {
		if l.msg == msg {
			out = append(out, l)
		}
	}
	return out
}

// text renders every line and attribute, to search for something that must not be there.
func (r *logRecorder) text() string {
	var b strings.Builder
	for _, l := range r.all() {
		fmt.Fprintf(&b, "%s %s %v\n", l.level, l.msg, l.attrs)
	}
	return b.String()
}

// dialLogged is dialFake with a logger.
func dialLogged(t *testing.T, f *escptest.Switch, pass Password, log *slog.Logger, mutate ...func(*Config)) *Client {
	t.Helper()
	cfg := Config{Host: "127.0.0.1", Username: "admin", Password: pass, Timeout: 150 * time.Millisecond, Retries: 1,
		SwitchPort: f.Port(), BroadcastAddr: escptest.Loopback, Logger: log}
	for _, m := range mutate {
		m(&cfg)
	}
	c := newBoundClient(t, cfg)
	c.allowLoopback = true
	t.Cleanup(func() { c.Close() })
	return c
}

func TestLoggerSeesTheRequestsAndRepliesAndNeverASecret(t *testing.T) {
	f := escptest.Start(t)
	log, rec := newLogRecorder()
	c := dialLogged(t, f, escptest.Password, log)
	ctx := context.Background()
	if err := c.SetLED(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LED(ctx); err != nil {
		t.Fatal(err)
	}

	rec.assertIdentified(t)
	if got := rec.named("session established"); len(got) != 1 || got[0].level != slog.LevelInfo {
		t.Errorf("session lines = %+v, want one Info", got)
	}
	writes := rec.named("write")
	if len(writes) != 1 || writes[0].level != slog.LevelInfo || writes[0].attrs["operation"] != "SetLED" || writes[0].attrs["impact"] != "cosmetic" {
		t.Errorf("write lines = %+v, want one Info naming SetLED and its impact", writes)
	}
	var sets, gets, replies, keyReplies int
	for _, l := range rec.named("request sent") {
		if l.level != slog.LevelDebug || l.attrs["host"] != "127.0.0.1" {
			t.Errorf("request line = %+v, want Debug with the switch's host", l)
		}
		switch l.attrs["op"] {
		case "set":
			sets++
		case "get":
			gets++
			if types, _ := l.attrs["tlvs"].([]int); len(types) != 1 || types[0] != int(wire.TLVLED) {
				t.Errorf("the LED read names TLV types %v, want [%d]", l.attrs["tlvs"], wire.TLVLED)
			}
		}
	}
	for _, l := range rec.named("reply received") {
		// the reply to the key exchange carries the vendor's "key accepted" code, all others none
		code := l.attrs["error_code"]
		if code == int64(wire.ErrCodeKeyAccepted) {
			keyReplies++
		} else if code != int64(0) {
			t.Errorf("reply line = %+v, want error code 0", l)
		}
		if l.level != slog.LevelDebug {
			t.Errorf("reply line = %+v, want Debug", l)
		}
		replies++
	}
	if keyReplies != 1 {
		t.Errorf("%d replies carry the key-accepted code, want one", keyReplies)
	}
	if sets < 2 || gets != 1 || replies < 3 { // the key exchange and the write, then the read
		t.Errorf("%d sets, %d gets, %d replies; want at least two sets (key exchange, write), one get and a reply to each", sets, gets, replies)
	}

	// No value of any TLV is logged: not the password, not the user name, not the session key.
	text := rec.text()
	for name, secret := range map[string]string{"password": escptest.Password, "user name": escptest.Username, "session key": string(c.session)} {
		if secret == "" || strings.Contains(text, secret) {
			t.Errorf("the log contains the %s (or there was none to look for): %q", name, secret)
		}
	}
}

func TestClientWithoutALoggerDiscards(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if c.log == nil {
		t.Fatal("a client without a logger must still have one that discards")
	}
	if _, err := c.LED(context.Background()); err != nil { // would panic on a nil logger
		t.Fatal(err)
	}
}

func TestLoggerAttachesTheMACOfAPinnedSwitch(t *testing.T) {
	f := escptest.Start(t)
	log, rec := newLogRecorder()
	c, _ := dialMAC(t, f, "127.0.0.1", wantMAC, nil)
	c.log = clientLogger(Config{Host: "127.0.0.1", MAC: wantMAC, Logger: log})
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := rec.named("request sent")
	if len(lines) == 0 || lines[0].attrs["mac"] != wantMAC.String() || lines[0].attrs["host"] != "127.0.0.1" {
		t.Errorf("request lines = %+v, want the host and the MAC attached", lines)
	}
}

func TestLoggerWarnsAboutALostPacketAndTheRetry(t *testing.T) {
	f := escptest.Start(t)
	f.DropFirst(2) // the first discovery probe goes out twice (to the switch and to the broadcast address)
	log, rec := newLogRecorder()
	c := dialLogged(t, f, "secret", log)
	if _, err := c.LED(context.Background()); err != nil {
		t.Fatal(err)
	}
	lost := rec.named("no reply")
	if len(lost) != 1 || lost[0].level != slog.LevelWarn || lost[0].attrs["attempt"] != int64(1) || lost[0].attrs["attempts"] != int64(2) {
		t.Errorf("no-reply lines = %+v, want one Warn for attempt 1 of 2", lost)
	}
}

func TestLoggerNamesAWriteTheGateRefused(t *testing.T) {
	f := escptest.Start(t)
	log, rec := newLogRecorder()
	refuse := func(c *Config) {
		c.WriteGate = func(context.Context, InfoReader, Operation) error { return errors.New("not today") }
	}
	c := dialLogged(t, f, "secret", log, refuse)
	if err := c.SetLED(context.Background(), false); err == nil {
		t.Fatal("the gate refuses, so the write must fail")
	}
	refused := rec.named("write refused by the gate")
	if len(refused) != 1 || refused[0].level != slog.LevelDebug || refused[0].attrs["operation"] != "SetLED" {
		t.Errorf("refusal lines = %+v", refused)
	}
	if len(rec.named("write")) != 0 || f.Received() != 0 {
		t.Errorf("a refused write is neither announced nor sent: %d write lines, %d packets", len(rec.named("write")), f.Received())
	}
}

func TestLoggerFollowsAConfigurationDownload(t *testing.T) {
	t.Run("a lost last packet is asked for again", func(t *testing.T) {
		f := escptest.Start(t)
		f.SetBackupScript(escptest.BackupDropSecond)
		log, rec := newLogRecorder()
		c := dialLogged(t, f, "secret", log)
		if _, err := c.ConfigBackup(context.Background()); err != nil {
			t.Fatal(err)
		}
		missing := rec.named("final fragment missing; asking again")
		if len(missing) != 1 || missing[0].level != slog.LevelWarn || missing[0].attrs["packets"] != int64(1) {
			t.Errorf("missing-fragment lines = %+v, want one Warn after the first packet", missing)
		}
		frags := rec.named("fragment received")
		if len(frags) < 3 || frags[0].attrs["packet"] != int64(1) || frags[0].attrs["more"] != true {
			t.Errorf("fragment lines = %+v", frags)
		}
	})
	t.Run("a damaged download is repeated and says why", func(t *testing.T) {
		f := escptest.Start(t)
		f.SetBackupScript(escptest.BackupCorrupt, escptest.BackupOK, escptest.BackupOK, escptest.BackupOK)
		log, rec := newLogRecorder()
		c := dialLogged(t, f, "secret", log)
		if _, err := c.ConfigBackup(context.Background()); err != nil {
			t.Fatal(err)
		}
		again := rec.named("configuration download not usable; repeating")
		if len(again) != 1 || again[0].level != slog.LevelWarn || again[0].attrs["round"] != int64(1) || !strings.Contains(again[0].attrs["reason"].(string), "differ") {
			t.Errorf("repeat lines = %+v, want one Warn for round 1 saying the downloads differ", again)
		}
	})
}

func TestLoggerFollowsTheLookupByMAC(t *testing.T) {
	f := escptest.Start(t)
	log, rec := newLogRecorder()
	misses := 1
	c, _ := dialMAC(t, f, "", wantMAC, func(m MAC) (Info, error) {
		if misses > 0 {
			misses--
			return Info{}, ErrNotFound
		}
		return Info{IP: netip.MustParseAddr("127.0.0.1"), MAC: m}, nil
	})
	c.log = log
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.named("lookup by MAC found nobody; asking once more"); len(got) != 1 || got[0].level != slog.LevelWarn {
		t.Errorf("second-lookup lines = %+v", got)
	}
	if got := rec.named("switch located by MAC"); len(got) != 1 || got[0].level != slog.LevelInfo || got[0].attrs["ip"] != "127.0.0.1" {
		t.Errorf("located lines = %+v", got)
	}
}

func TestLoggerReportsASessionReopenedAfterSilence(t *testing.T) {
	f := escptest.Start(t)
	log, rec := newLogRecorder()
	c := dialLogged(t, f, "secret", log)
	if _, err := c.LED(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.ClearSession() // the switch forgets the key, as after a reboot: the next request gets no reply
	if _, err := c.LED(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rec.named("no reply; opening a new session"); len(got) != 1 || got[0].level != slog.LevelWarn || got[0].attrs["lookup_by_mac"] != false {
		t.Errorf("new-session lines = %+v", got)
	}
	if got := rec.named("session established"); len(got) != 2 {
		t.Errorf("%d sessions established, want 2", len(got))
	}
}

func TestLoggerFollowsALeaseAndAMove(t *testing.T) {
	t.Run("the wait for a lease", func(t *testing.T) {
		f := escptest.Start(t)
		f.SetLease(escptest.Lease{IP: escptest.DefaultLease.IP, Mask: escptest.DefaultLease.Mask, Gateway: escptest.DefaultLease.Gateway, Silence: 6})
		c, _ := dialLeasing(t, f)
		log, rec := newLogRecorder()
		c.log = log
		if err := c.EnableDHCP(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := rec.named("no lease reported yet; asking again"); len(got) == 0 || got[0].level != slog.LevelDebug {
			t.Errorf("waiting lines = %+v, want Debug lines while the switch is silent", got)
		}
		if got := rec.named("lease obtained"); len(got) != 1 || got[0].level != slog.LevelInfo {
			t.Errorf("lease lines = %+v", got)
		}
	})
	t.Run("a move whose reply is lost", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.OnSystemSet = escptest.SystemSetLeaves })
		log, rec := newLogRecorder()
		c := dialLogged(t, f, "secret", log)
		if err := c.SetIPConfig(context.Background(), lostReplyWanted); err != nil {
			t.Fatal(err)
		}
		if got := rec.named("the write got no reply; asking the switch what it has"); len(got) != 1 || got[0].level != slog.LevelWarn {
			t.Errorf("settle lines = %+v", got)
		}
	})
	t.Run("a switch that is not at the new address yet", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.StaleAfterSet = 2 })
		log, rec := newLogRecorder()
		c := dialLogged(t, f, "secret", log)
		if err := c.SetIPConfig(context.Background(), lostReplyWanted); err != nil {
			t.Fatal(err)
		}
		if got := rec.named("the switch is not at the new address yet; asking again"); len(got) == 0 || got[0].level != slog.LevelDebug {
			t.Errorf("waiting lines = %+v", got)
		}
	})
}

func TestDiscoveryLogsItsProbesAndWhatItFound(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, answerAsOneSwitch)
	log, rec := newLogRecorder()
	_, err := discoverWith(context.Background(), DiscoverOptions{Window: 200 * time.Millisecond, Logger: log}, loopbackEnv(t, r, hostPort,
		netInterface{Name: "en0", MAC: macOne, Broadcasts: lo}))
	if err != nil {
		t.Fatal(err)
	}
	rec.assertIdentified(t)
	probes := rec.named("discovery probes")
	if len(probes) != 1 || probes[0].level != slog.LevelDebug || probes[0].attrs["interfaces"] != int64(1) || probes[0].attrs["probes"] != int64(1) {
		t.Errorf("probe lines = %+v", probes)
	}
	if found := rec.named("discovery replies"); len(found) != 1 || found[0].attrs["switches"] != int64(1) {
		t.Errorf("reply lines = %+v, want the one switch the responder is", found)
	}
}

func TestLocateLogsWhereItFoundTheSwitch(t *testing.T) {
	hostPort := escptest.FreeUDPPort(t)
	r := startResponder(t, hostPort, answerAsOneSwitch)
	log, rec := newLogRecorder()
	if _, err := locateWith(context.Background(), MAC(oneSwitchMAC), DiscoverOptions{Window: 200 * time.Millisecond, Logger: log}, loopbackEnv(t, r, hostPort,
		netInterface{Name: "en0", MAC: macOne, Broadcasts: lo})); err != nil {
		t.Fatal(err)
	}
	rec.assertIdentified(t)
	if got := rec.named("lookup by MAC found the switch"); len(got) != 1 || got[0].level != slog.LevelDebug || got[0].attrs["mac"] != MAC(oneSwitchMAC).String() || got[0].attrs["ip"] != "192.0.2.30" {
		t.Errorf("lookup lines = %+v", got)
	}
}

// oneSwitchMAC is the switch answerAsOneSwitch pretends to be.
var oneSwitchMAC = [6]byte{0xa8, 0x42, 0xa1, 0, 0, 0x30}

func answerAsOneSwitch(h wire.Header) [][]byte {
	return [][]byte{packet(wire.OpReply, h.Seq, discoverTLVs(oneSwitchMAC, [4]byte{192, 0, 2, 30}, "sw-a"))}
}

// The client hands Locate its logger before identifying it, so a lookup the client makes is not
// marked twice.
func TestClientLoggerIsIdentifiedByTheClientNotBeforeIt(t *testing.T) {
	log, rec := newLogRecorder()
	base := clientLogger(Config{Host: "192.0.2.1", MAC: MAC(oneSwitchMAC), Logger: log})
	base.Info("probe")
	if got := rec.all(); len(got) != 1 || got[0].attrs[componentKey] != nil || got[0].attrs["host"] != "192.0.2.1" {
		t.Fatalf("lines = %+v, want host attached and no component yet", got)
	}
	identified(base).Info("probe")
	if lines := rec.all(); lines[1].attrs[componentKey] != componentName || len(lines[1].dupes) > 0 {
		t.Errorf("identified line = %+v", lines[1])
	}
}
