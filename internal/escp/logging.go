package escp

import (
	"log/slog"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// The library logs through the *slog.Logger the caller passes in Config.Logger (clients) or
// DiscoverOptions.Logger (Discover and Locate), and nowhere else: no fmt.Print*, no log.* (a
// lint rule enforces it). The zero value discards. The messages use the ...Context methods so a
// handler can reach what the caller put in the context (the provider's handler finds the
// Terraform logger there).
//
// Levels: Debug is protocol detail (each request and reply, each fragment), Info is lifecycle
// (a session, the switch located, a write sent), Warn is a recoverable anomaly (no reply, a
// session reopened, a download repeated). Failures are returned, never also logged as errors.
//
// Secrets never reach a message: TLV values are not logged, only their types and the number of
// bytes they carry, so neither the password, the username nor the session key can appear.
// Every line carries component=escp, which identifies the library the way a logger name does.
// Attribute names are stable (op, seq, attempt, attempts, tlvs, bytes, error_code, host, mac,
// operation, impact, ports, round) and are what a consumer filters on; new ones may be added, none
// is renamed or removed before a major version.

// componentKey and componentName identify every line the library logs, like the logger name of
// log4j: a consumer that ships its logs elsewhere filters on component=escp to keep or drop them.
const (
	componentKey  = "component"
	componentName = "escp"
)

// orDiscard is l, or a logger that discards everything when l is nil.
func orDiscard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}
	return l
}

// identified marks l as the library's: every line carries component=escp, exactly once, so it is
// applied where the caller's logger enters the library (the client, Discover, Locate) and nowhere
// below.
func identified(l *slog.Logger) *slog.Logger {
	return l.With(componentKey, componentName)
}

// tlvTypes lists the types of the TLVs of a packet, in order: what a request asked for or a reply
// carried, without the values.
func tlvTypes(tlvs []wire.TLV) []int {
	out := make([]int, len(tlvs))
	for i, t := range tlvs {
		out[i] = int(t.Type)
	}
	return out
}

// tlvBytes is the number of value bytes the TLVs carry.
func tlvBytes(tlvs []wire.TLV) int {
	n := 0
	for _, t := range tlvs {
		n += len(t.Value)
	}
	return n
}
