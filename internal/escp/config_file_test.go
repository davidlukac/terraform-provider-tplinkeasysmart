package escp

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigFileSizeHashAndBase64(t *testing.T) {
	f := ConfigFile("abc")
	if f.Size() != 3 {
		t.Errorf("Size = %d, want 3", f.Size())
	}
	// SHA-256 and base64 of "abc" (FIPS 180-2 test vector, RFC 4648).
	if want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; f.SHA256() != want {
		t.Errorf("SHA256 = %s, want %s", f.SHA256(), want)
	}
	if f.Base64() != "YWJj" {
		t.Errorf("Base64 = %s, want YWJj", f.Base64())
	}
	if ConfigFile(nil).Size() != 0 {
		t.Error("an empty file has size 0")
	}
}

// The file holds the account block of the switch: it must not reach a log or an error by accident.
func TestConfigFileNeverPrintsItsBytes(t *testing.T) {
	const marker = "ACCOUNT-BLOCK-MARKER"
	f := ConfigFile(marker)
	for name, got := range map[string]string{
		"%v":     fmt.Sprintf("%v", f),
		"%s":     fmt.Sprintf("%s", f), //nolint:staticcheck // the verb is what is under test
		"%x":     fmt.Sprintf("%x", f),
		"%#v":    fmt.Sprintf("%#v", f),
		"struct": fmt.Sprintf("%+v", struct{ File ConfigFile }{f}),
	} {
		if strings.Contains(got, marker) || strings.Contains(got, fmt.Sprintf("%x", marker)) {
			t.Errorf("%s leaks the file: %s", name, got)
		}
	}
	if got := f.String(); !strings.Contains(got, "20 bytes") || !strings.Contains(got, f.SHA256()) {
		t.Errorf("String = %q, want the size and the hash", got)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("backup", "file", f)
	if strings.Contains(buf.String(), marker) || !strings.Contains(buf.String(), f.SHA256()) {
		t.Errorf("the log line = %s, want the hash and not the bytes", buf.String())
	}
}
