package escp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

func TestConfigBackupReassemblesFragments(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	got, err := c.ConfigBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := escptest.ConfigFile(); !bytes.Equal(got, want) {
		t.Fatalf("reassembled %d bytes, want %d identical bytes", len(got), len(want))
	}
}

func TestConfigBackupDoesNotWriteSettings(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if _, err := c.ConfigBackup(context.Background()); err != nil {
		t.Fatal(err)
	}
	led, _, sets := f.State()
	if !led || sets != 0 {
		t.Fatalf("a backup must not change anything: led=%v sets=%d", led, sets)
	}
}

func TestConfigBackupRejectsWrongCredentials(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "wrong")
	_, err := c.ConfigBackup(context.Background())
	if err == nil {
		t.Fatal("expected an error for rejected credentials")
	}
}

func TestConfigBackupRejectsNonConfigData(t *testing.T) {
	// escptest.BackupDropFirst delivers only the final fragment: a partial file must not pass for a backup.
	for _, mode := range []escptest.BackupMode{escptest.BackupBadMagic, escptest.BackupShort, escptest.BackupDropFirst} {
		t.Run(string(mode), func(t *testing.T) {
			f := escptest.Start(t)
			f.SetBackupMode(mode)
			c := dialFake(t, f, "secret")
			if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestConfigBackupMissingFragmentIsAnError(t *testing.T) {
	// The second fragment never arrives: the request is retried, then fails.
	f := escptest.Start(t)
	f.SetBackupMode(escptest.BackupDropSecond)
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 1
	c.cfg.Timeout = 150 * time.Millisecond
	if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
}

func TestConfigBackupNoReply(t *testing.T) {
	f := escptest.Start(t)
	f.SetBackupMode(escptest.BackupSilent)
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 0
	c.cfg.Timeout = 150 * time.Millisecond
	if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
}

// The key exchange completes at once; the backup request is then left
// unanswered until the context expires, which must end the call with the
// context's error instead of retrying for the full per-attempt timeout.
func TestConfigBackupHonoursContext(t *testing.T) {
	f := escptest.Start(t)
	f.SetBackupMode(escptest.BackupSilent)
	c := newBoundClient(t, Config{Host: "127.0.0.1", Username: "admin", Password: "secret", Timeout: 30 * time.Second,
		SwitchPort: f.Port(), BroadcastAddr: escptest.Loopback})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	_, err := c.ConfigBackup(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the call ignored the context deadline and took %v", took)
	}
}

func TestConfigBackupRefusesAnEndlessFragmentedReply(t *testing.T) {
	// A sender that keeps the fragment flag set must not make the client buffer
	// without limit.
	f := escptest.Start(t)
	f.SetBackupMode(escptest.BackupFlood)
	c := dialFake(t, f, "secret")
	c.cfg.Timeout = 2 * time.Second
	if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrReplyTooLarge) {
		t.Fatalf("want ErrReplyTooLarge, got %v", err)
	}
}

// A wrong password is a refusal by the switch, for the backup as for every write.
func TestConfigBackupRefusalIsARejection(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "wrong")
	_, err := c.ConfigBackup(context.Background())
	var refused *RejectedError
	if !errors.Is(err, ErrRejected) || !errors.As(err, &refused) || refused.Code != 5 {
		t.Fatalf("want a *RejectedError with code 5, got %v", err)
	}
}

// A packet lost once, as on a real network (2 of 228 downloads), costs a repeated download, never a damaged
// file: whatever ConfigBackup returns is the whole file.
func TestConfigBackupRepeatsAfterALossAndReturnsTheWholeFile(t *testing.T) {
	for name, script := range map[string][]escptest.BackupMode{
		"first packet lost":             {escptest.BackupDropFirst},
		"last packet lost":              {escptest.BackupDropSecond},
		"one byte wrong, in the first":  {escptest.BackupCorrupt},
		"one byte wrong, in the second": {escptest.BackupOK, escptest.BackupCorrupt},
		"a packet delivered twice":      {escptest.BackupDuplicate},
		"the second download lost":      {escptest.BackupOK, escptest.BackupDropFirst},
	} {
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			f.SetBackupScript(script...)
			c := dialFake(t, f, "secret")
			c.cfg.Timeout = 150 * time.Millisecond // the lost last packet is waited for once
			got, err := c.ConfigBackup(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if want := escptest.ConfigFile(); !bytes.Equal(got, want) {
				t.Fatalf("got %d bytes that differ from the %d-byte file", len(got), len(want))
			}
		})
	}
}

// When every round fails there is no file, and the error says why.
func TestConfigBackupFailsRatherThanReturnAPartialFile(t *testing.T) {
	f := escptest.Start(t)
	// each round: a damaged first download, then a good second one, so the two never agree
	f.SetBackupScript(escptest.BackupCorrupt, escptest.BackupOK, escptest.BackupCorrupt, escptest.BackupOK, escptest.BackupCorrupt, escptest.BackupOK)
	c := dialFake(t, f, "secret") // three rounds with the default two retries
	got, err := c.ConfigBackup(context.Background())
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "two downloads differ") {
		t.Fatalf("got %v, want ErrInvalidConfig saying that the downloads differ", err)
	}
	if got != nil {
		t.Fatalf("a failed backup returned %d bytes", len(got))
	}
}

// A second download that gets no answer ends the call with that error, not with the unverified first one.
func TestConfigBackupWithoutAnswerToTheSecondDownloadFails(t *testing.T) {
	f := escptest.Start(t)
	f.SetBackupMode(escptest.BackupSilent) // from the second request on, nothing answers
	f.SetBackupScript(escptest.BackupOK)
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 1
	c.cfg.Timeout = 100 * time.Millisecond
	got, err := c.ConfigBackup(context.Background())
	if !errors.Is(err, ErrNoReply) || got != nil {
		t.Fatalf("got %d bytes and %v, want no file and ErrNoReply", len(got), err)
	}
}

func TestAssembleConfig(t *testing.T) {
	magic := []byte{0x23, 0x89, 0x23, 0x89}
	with := func(n int) []byte { return append(append([]byte(nil), magic...), make([]byte, n-len(magic))...) }
	body := func(n int) []byte { return make([]byte, n) }
	for _, tc := range []struct {
		name    string
		parts   [][]byte
		wantLen int    // 0: refused
		errPart string // what the refusal says
	}{
		{"two packets, as the switch sends it", [][]byte{with(1400), body(858)}, 2258, ""},
		{"a small file in one packet", [][]byte{with(100)}, 100, ""},
		{"three equal packets and a short one", [][]byte{with(1400), body(1400), body(1400), body(10)}, 4210, ""},
		{"no packets", nil, 0, "0 bytes in 0 packets"},
		{"only the magic", [][]byte{magic}, 0, "no configuration magic"},
		{"the first packet lost", [][]byte{body(858)}, 0, "no configuration magic"},
		{"a shorter packet before the last", [][]byte{with(1400), body(700), body(858)}, 0, "packet 2 has 700 bytes, packet 1 has 1400"},
		{"a longer last packet", [][]byte{with(1000), body(1400)}, 0, "the last packet has 1400 bytes, more than the first, 1000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := assembleConfig(tc.parts)
			if tc.wantLen == 0 {
				if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), tc.errPart) {
					t.Fatalf("got %v, want ErrInvalidConfig containing %q", err, tc.errPart)
				}
				return
			}
			if err != nil || len(got) != tc.wantLen {
				t.Fatalf("got %d bytes and %v, want %d bytes", len(got), err, tc.wantLen)
			}
		})
	}
}
