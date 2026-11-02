package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
)

// aimedAt returns the dependencies that reach the fake switch instead of the network.
func aimedAt(t *testing.T, f *escptest.Switch) deps {
	t.Helper()
	return deps{
		dial: func(cfg escp.Config) (*escp.Client, error) {
			cfg.SwitchPort, cfg.HostPort, cfg.BroadcastAddr = f.UDPPort(), escptest.FreeUDPPort(t), escptest.Loopback()
			cfg.Timeout = 200 * time.Millisecond
			return escp.Dial(cfg)
		},
		create: createExclusive,
	}
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func goodEnv() map[string]string {
	return map[string]string{envHost: "127.0.0.1", envUsername: escptest.Username, envPassword: escptest.Password}
}

func TestBackupWritesTheWholeVerifiedFile(t *testing.T) {
	f := escptest.Start(t)
	path := filepath.Join(t.TempDir(), "switch.bin")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-out", path}, envOf(goodEnv()), &stdout, &stderr, aimedAt(t, f)); code != exitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := escptest.BackupFile()
	if !bytes.Equal(got, want) {
		t.Fatalf("saved %d bytes, want the %d-byte file", len(got), len(want))
	}
	sum := sha256.Sum256(want)
	if line := "sha256=" + hex.EncodeToString(sum[:]); !strings.Contains(stdout.String(), line) || !strings.Contains(stdout.String(), path+" 2258 bytes ") {
		t.Fatalf("result line %q lacks the size and %s", stdout.String(), line)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, err %v, want 0600", info.Mode().Perm(), err)
	}
	if strings.Contains(stdout.String()+stderr.String(), escptest.Password) {
		t.Fatal("the password was printed")
	}
}

// A packet lost once is repeated inside the library: the file is still the whole one.
func TestBackupSurvivesALostPacket(t *testing.T) {
	f := escptest.Start(t)
	f.SetBackupScript(escptest.BackupDropFirst)
	path := filepath.Join(t.TempDir(), "switch.bin")
	if code := run([]string{"-out", path}, envOf(goodEnv()), io.Discard, io.Discard, aimedAt(t, f)); code != exitOK {
		t.Fatalf("exit %d", code)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, escptest.BackupFile()) {
		t.Fatalf("saved %d bytes, want the whole file", len(got))
	}
}

// When the library cannot get the whole file, no file is written, not even an empty one.
func TestNoFileWhenTheBackupFails(t *testing.T) {
	for name, setup := range map[string]func(*escptest.Switch, map[string]string){
		"packets keep getting lost": func(f *escptest.Switch, _ map[string]string) { f.SetBackupMode(escptest.BackupDropFirst) },
		"wrong password":            func(_ *escptest.Switch, env map[string]string) { env[envPassword] = "pw-that-must-not-be-printed" },
	} {
		t.Run(name, func(t *testing.T) {
			f, env := escptest.Start(t), goodEnv()
			setup(f, env)
			path := filepath.Join(t.TempDir(), "switch.bin")
			var stderr bytes.Buffer
			if code := run([]string{"-out", path}, envOf(env), io.Discard, &stderr, aimedAt(t, f)); code != exitFailed {
				t.Fatalf("exit %d, want %d", code, exitFailed)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a file exists after a failed backup: %v", err)
			}
			if !strings.Contains(stderr.String(), "no file written") || strings.Contains(stderr.String(), "pw-that-must-not-be-printed") {
				t.Fatalf("stderr: %q", stderr.String())
			}
		})
	}
}

func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	f := escptest.Start(t)
	path := filepath.Join(t.TempDir(), "switch.bin")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"-out", path}, envOf(goodEnv()), io.Discard, &stderr, aimedAt(t, f)); code != exitFailed {
		t.Fatalf("exit %d", code)
	}
	if got, _ := os.ReadFile(path); string(got) != "keep" {
		t.Fatalf("the existing file was changed to %q", got)
	}
	if !strings.Contains(stderr.String(), "not saved") {
		t.Fatalf("stderr: %q", stderr.String())
	}
}

type failingWriter struct{ failWrite, failClose bool }

func (w failingWriter) Write(p []byte) (int, error) {
	if w.failWrite {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

func (w failingWriter) Close() error {
	if w.failClose {
		return errors.New("close failed")
	}
	return nil
}

func TestAFailedWriteIsReportedAndExitsNonZero(t *testing.T) {
	for name, w := range map[string]failingWriter{"write": {failWrite: true}, "close": {failClose: true}} {
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			d := aimedAt(t, f)
			d.create = func(string) (io.WriteCloser, error) { return w, nil }
			var stderr bytes.Buffer
			if code := run([]string{"-out", filepath.Join(t.TempDir(), "x.bin")}, envOf(goodEnv()), io.Discard, &stderr, d); code != exitFailed {
				t.Fatalf("exit %d", code)
			}
			if !strings.Contains(stderr.String(), "not saved") {
				t.Fatalf("stderr: %q", stderr.String())
			}
		})
	}
}

func TestUsageErrors(t *testing.T) {
	noDial := deps{dial: func(escp.Config) (*escp.Client, error) { t.Fatal("dialed"); return nil, nil }, create: createExclusive}
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
		want string
	}{
		"no -out":                    {nil, goodEnv(), "-out FILE is required"},
		"no host and no MAC":         {[]string{"-out", "x"}, map[string]string{envUsername: "u", envPassword: "p"}, "-host or -mac"},
		"no credentials":             {[]string{"-out", "x"}, map[string]string{envHost: "h"}, envUsername + " and " + envPassword},
		"a bad MAC":                  {[]string{"-out", "x", "-mac", "not-a-mac"}, goodEnv(), "MAC"},
		"an unknown flag":            {[]string{"-nope"}, goodEnv(), "flag provided but not defined"},
		"credentials as an argument": {[]string{"-out", "x", "-password", "p"}, goodEnv(), "flag provided but not defined"},
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(tc.args, envOf(tc.env), io.Discard, &stderr, noDial); code != exitUsage {
				t.Fatalf("exit %d, want %d (stderr %q)", code, exitUsage, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr %q lacks %q", stderr.String(), tc.want)
			}
		})
	}
}

// A MAC names the switch: the library is given it, and an address that is wrong is found again by it.
func TestTheMACIsPassedToTheLibrary(t *testing.T) {
	f := escptest.Start(t)
	var got escp.Config
	d := aimedAt(t, f)
	inner := d.dial
	d.dial = func(cfg escp.Config) (*escp.Client, error) { got = cfg; return inner(cfg) }
	env := goodEnv()
	env[envMAC] = "aa:bb:cc:dd:ee:ff"
	// the fake's default MAC differs, so the library refuses the device: the point is only that the MAC arrived
	_ = run([]string{"-out", filepath.Join(t.TempDir(), "x.bin")}, envOf(env), io.Discard, io.Discard, d)
	if want, _ := escp.ParseMAC("aa:bb:cc:dd:ee:ff"); got.MAC != want {
		t.Fatalf("the client was configured with MAC %v, want %v", got.MAC, want)
	}
}

func TestDialFailureIsAFailure(t *testing.T) {
	d := deps{dial: func(escp.Config) (*escp.Client, error) { return nil, errors.New("no route") }, create: createExclusive}
	var stderr bytes.Buffer
	if code := run([]string{"-out", filepath.Join(t.TempDir(), "x.bin")}, envOf(goodEnv()), io.Discard, &stderr, d); code != exitFailed || !strings.Contains(stderr.String(), "no route") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}
