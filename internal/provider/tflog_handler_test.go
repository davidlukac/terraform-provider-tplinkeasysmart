package provider

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/hashicorp/terraform-plugin-log/tflogtest"
)

// logged runs fn with a logger writing through the handler into a tflog test root logger and returns
// the entries it produced.
func logged(t *testing.T, logger *slog.Logger, fn func(ctx context.Context, l *slog.Logger)) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	ctx := tflogtest.RootLogger(context.Background(), &out)
	fn(ctx, logger)
	entries, err := tflogtest.MultilineJSONDecode(&out)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestTflogHandlerMapsLevelsMessagesAndAttributes(t *testing.T) {
	entries := logged(t, newTflogLogger(), func(ctx context.Context, l *slog.Logger) {
		l.Log(ctx, slog.LevelDebug-4, "trace line")
		l.DebugContext(ctx, "debug line", "seq", 7, "tlvs", []int{4352, 4353})
		l.InfoContext(ctx, "info line", "ip", "192.0.2.13")
		l.WarnContext(ctx, "warn line")
		l.ErrorContext(ctx, "error line")
	})
	want := []struct{ level, msg string }{{"trace", "trace line"}, {"debug", "debug line"}, {"info", "info line"}, {"warn", "warn line"}, {"error", "error line"}}
	if len(entries) != len(want) {
		t.Fatalf("%d entries, want %d: %v", len(entries), len(want), entries)
	}
	for i, w := range want {
		if entries[i]["@level"] != w.level || entries[i]["@message"] != w.msg {
			t.Errorf("entry %d = level %v message %v, want %s %q", i, entries[i]["@level"], entries[i]["@message"], w.level, w.msg)
		}
	}
	if entries[1]["seq"] != float64(7) {
		t.Errorf("seq = %v, want 7", entries[1]["seq"])
	}
	if tlvs, _ := entries[1]["tlvs"].([]any); len(tlvs) != 2 || tlvs[0] != float64(4352) {
		t.Errorf("tlvs = %v, want [4352 4353]", entries[1]["tlvs"])
	}
	if entries[2]["ip"] != "192.0.2.13" {
		t.Errorf("ip = %v", entries[2]["ip"])
	}
}

func TestTflogHandlerKeepsAttributesAndGroups(t *testing.T) {
	entries := logged(t, newTflogLogger(), func(ctx context.Context, l *slog.Logger) {
		client := l.With("host", "192.0.2.13").WithGroup("switch").With("mac", "aa:bb")
		client.InfoContext(ctx, "write", "operation", "SetLED", slog.Group("ports", slog.Int("count", 2)), slog.Group("", slog.String("bare", "x")), slog.Attr{})
		// slog.Logger.WithGroup skips an empty name before it reaches the handler, so ask the handler.
		slog.New(tflogHandler{}.WithGroup("")).InfoContext(ctx, "no group", "k", "v")
	})
	if len(entries) != 2 {
		t.Fatalf("%d entries: %v", len(entries), entries)
	}
	e := entries[0]
	for k, want := range map[string]any{"host": "192.0.2.13", "switch.mac": "aa:bb", "switch.operation": "SetLED", "switch.ports.count": float64(2), "switch.bare": "x"} {
		if e[k] != want {
			t.Errorf("field %q = %v, want %v (entry %v)", k, e[k], want, e)
		}
	}
	if _, ok := e["switch."]; ok {
		t.Errorf("an empty attribute must not become a field: %v", e)
	}
	if entries[1]["k"] != "v" {
		t.Errorf("an empty group name opens no group: %v", entries[1])
	}
}

// A line logged with a context that has no Terraform logger goes nowhere and breaks nothing.
func TestTflogHandlerWithoutATerraformLoggerIsSilent(t *testing.T) {
	newTflogLogger().InfoContext(context.Background(), "nobody listens")
}

func TestTflogHandlerResolvesLogValuers(t *testing.T) {
	entries := logged(t, newTflogLogger(), func(ctx context.Context, l *slog.Logger) {
		l.InfoContext(ctx, "secretive", "password", secretValue{})
	})
	if len(entries) != 1 || entries[0]["password"] != "[redacted]" {
		t.Errorf("entries = %v, want the LogValuer resolved to its redacted form", entries)
	}
}

type secretValue struct{}

func (secretValue) LogValue() slog.Value { return slog.StringValue("[redacted]") }
