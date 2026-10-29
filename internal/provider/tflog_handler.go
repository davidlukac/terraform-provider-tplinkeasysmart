package provider

import (
	"context"
	"log/slog"
	"strings"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// tflogHandler is the slog.Handler the provider gives the ESCP library (escp.Config.Logger), so what
// the client does shows up in Terraform's log: `TF_LOG=DEBUG` prints every request and reply (types
// and sizes, never values), `TF_LOG=INFO` the sessions, lookups and writes. A record is written
// with the context of the call that logged it, which carries Terraform's logger and attributes the
// line to the resource operation that caused it. Levels map one to one (below Debug is Trace);
// attributes become fields, with the names of groups as dotted prefixes.
type tflogHandler struct {
	// groups are the open groups, outermost first; fields holds the attributes added with
	// WithAttrs, already prefixed with the groups that were open then.
	groups []string
	fields map[string]any
}

// newTflogLogger is the logger the provider hands to every client it creates.
func newTflogLogger() *slog.Logger { return slog.New(tflogHandler{}) }

// Enabled is always true: tflog drops the lines the user's TF_LOG level does not ask for.
func (tflogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h tflogHandler) Handle(ctx context.Context, r slog.Record) error {
	fields := make(map[string]any, len(h.fields)+r.NumAttrs())
	for k, v := range h.fields {
		fields[k] = v
	}
	r.Attrs(func(a slog.Attr) bool { h.addField(fields, a); return true })
	switch {
	case r.Level >= slog.LevelError:
		tflog.Error(ctx, r.Message, fields)
	case r.Level >= slog.LevelWarn:
		tflog.Warn(ctx, r.Message, fields)
	case r.Level >= slog.LevelInfo:
		tflog.Info(ctx, r.Message, fields)
	case r.Level >= slog.LevelDebug:
		tflog.Debug(ctx, r.Message, fields)
	default:
		tflog.Trace(ctx, r.Message, fields)
	}
	return nil
}

func (h tflogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	fields := make(map[string]any, len(h.fields)+len(attrs))
	for k, v := range h.fields {
		fields[k] = v
	}
	for _, a := range attrs {
		h.addField(fields, a)
	}
	return tflogHandler{groups: h.groups, fields: fields}
}

func (h tflogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return tflogHandler{groups: append(append([]string(nil), h.groups...), name), fields: h.fields}
}

// addField stores one attribute under the open groups' prefix; a group-valued attribute is
// flattened, its own name joining the prefix.
func (h tflogHandler) addField(fields map[string]any, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Value.Kind() == slog.KindGroup {
		inner := h
		if a.Key != "" {
			inner = h.WithGroup(a.Key).(tflogHandler)
		}
		for _, g := range a.Value.Group() {
			inner.addField(fields, g)
		}
		return
	}
	if a.Equal(slog.Attr{}) { // an empty attribute is no attribute
		return
	}
	fields[strings.Join(append(append([]string(nil), h.groups...), a.Key), ".")] = a.Value.Any()
}
