package escp

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfigBackupReassemblesFragments(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	got, err := c.ConfigBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := fakeConfig(); !bytes.Equal(got, want) {
		t.Fatalf("reassembled %d bytes, want %d identical bytes", len(got), len(want))
	}
}

func TestConfigBackupDoesNotWriteSettings(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "secret")
	if _, err := c.ConfigBackup(context.Background()); err != nil {
		t.Fatal(err)
	}
	led, _, sets := f.state()
	if !led || sets != 0 {
		t.Fatalf("a backup must not change anything: led=%v sets=%d", led, sets)
	}
}

func TestConfigBackupRejectsWrongCredentials(t *testing.T) {
	f := startFakeSwitch(t)
	c := dialFake(t, f, "wrong")
	_, err := c.ConfigBackup(context.Background())
	if err == nil {
		t.Fatal("expected an error for rejected credentials")
	}
}

func TestConfigBackupRejectsNonConfigData(t *testing.T) {
	// "dropfirst" delivers only the final fragment: a partial file must not pass for a backup.
	for _, mode := range []string{"badmagic", "short", "dropfirst"} {
		t.Run(mode, func(t *testing.T) {
			f := startFakeSwitch(t)
			f.setBackupMode(mode)
			c := dialFake(t, f, "secret")
			if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestConfigBackupMissingFragmentIsAnError(t *testing.T) {
	// The second fragment never arrives: the request is retried, then fails.
	f := startFakeSwitch(t)
	f.setBackupMode("dropsecond")
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 1
	c.cfg.Timeout = 150 * time.Millisecond
	if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
}

func TestConfigBackupNoReply(t *testing.T) {
	f := startFakeSwitch(t)
	f.setBackupMode("silent")
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 0
	c.cfg.Timeout = 150 * time.Millisecond
	if _, err := c.ConfigBackup(context.Background()); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply, got %v", err)
	}
}

func TestConfigBackupHonoursContext(t *testing.T) {
	f := startFakeSwitch(t)
	f.setBackupMode("silent")
	c := dialFake(t, f, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.ConfigBackup(ctx); err == nil {
		t.Fatal("expected an error when the context expires")
	}
}
