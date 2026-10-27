package escp

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const secretText = "hunter2-not-for-logs"

func TestPasswordNeverPrints(t *testing.T) {
	p := Password(secretText)
	cfg := Config{Host: "192.0.2.11", Username: "admin", Password: p}
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"%v", fmt.Sprintf("%v", p)},
		{"Sprint", fmt.Sprint(p)},
		{"%q", fmt.Sprintf("%q", p)},
		{"%#v", fmt.Sprintf("%#v", p)},
		{"Config %v", fmt.Sprintf("%v", cfg)},
		{"Config %+v", fmt.Sprintf("%+v", cfg)},
		{"Config %#v", fmt.Sprintf("%#v", cfg)},
		{"error", fmt.Errorf("dialling with %+v", cfg).Error()},
	} {
		if strings.Contains(tc.got, secretText) {
			t.Errorf("%s leaks the password: %s", tc.name, tc.got)
		}
		if !strings.Contains(tc.got, "[redacted]") {
			t.Errorf("%s = %q, want it to say [redacted]", tc.name, tc.got)
		}
	}
}

func TestPasswordIsRedactedInStructuredLogs(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("dial", "password", Password(secretText), "config", Config{Password: Password(secretText)})
	if strings.Contains(buf.String(), secretText) {
		t.Fatalf("the log line leaks the password: %s", buf.String())
	}
	if !strings.Contains(buf.String(), `"password":"[redacted]"`) {
		t.Fatalf("the log line = %s, want the password redacted", buf.String())
	}
}

func TestPasswordRevealIsTheOnlyWayToTheText(t *testing.T) {
	if got := Password(secretText).Reveal(); got != secretText {
		t.Fatalf("Reveal = %q, want the password", got)
	}
	if Password("").Reveal() != "" {
		t.Fatal("an empty password reveals as empty")
	}
}
