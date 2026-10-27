package safety

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// Chain combines write gates into one that asks each in turn and stops at the first refusal. A
// nil gate is skipped, and Chain of nothing but nil gates is nil, which gates nothing. Put a gate
// that has an effect of its own (DryRun) last, so it only sees what the others allowed.
func Chain(gates ...escp.WriteGate) escp.WriteGate {
	var live []escp.WriteGate
	for _, g := range gates {
		if g != nil {
			live = append(live, g)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return func(ctx context.Context, sw escp.InfoReader, op escp.Operation) error {
		for _, g := range live {
			if err := g(ctx, sw, op); err != nil {
				return err
			}
		}
		return nil
	}
}

// ImpactError is the refusal of MaxImpact: the operation is above what the front end allows.
// Callers word the message and the way to proceed (a flag, a policy) for their users.
type ImpactError struct {
	Op  escp.Operation
	Max escp.Impact
}

// Error names the operation, its impact and the ceiling it exceeded.
func (e *ImpactError) Error() string {
	return fmt.Sprintf("%s has impact %s, above the allowed %s", e.Op.Name, e.Op.Impact, e.Max)
}

// MaxImpact returns a gate that refuses every operation whose Impact is above max. An operation
// that declares no valid Impact is refused too, so a writer that forgot to classify itself fails
// closed. A max that is not an impact class refuses everything. The refusal is an *ImpactError.
func MaxImpact(max escp.Impact) escp.WriteGate {
	return func(_ context.Context, _ escp.InfoReader, op escp.Operation) error {
		if !max.Valid() || !op.Impact.Valid() || op.Impact > max {
			return &ImpactError{Op: op, Max: max}
		}
		return nil
	}
}

// ErrDryRun is what the gate of a DryRun returns: the write was recorded and not sent. The
// client method returns it unchanged, so callers treat errors.Is(err, ErrDryRun) as success.
var ErrDryRun = errors.New("dry run: the write was recorded and not sent")

// DryRun records the operations a front end would perform without sending any of them. Use its
// Gate as the last gate of a client; run the commands as usual; read Operations. A method that
// first reads from the switch (SetHostname, SetIPConfig, SetPortPriorities) still does, and so
// does every method that names a port (it reads the switch's port count once, before the gate),
// so a dry run needs a reachable switch. A method that would send several writes records its
// operation once: the first refusal ends it.
// A DryRun is safe for concurrent use.
type DryRun struct {
	mu  sync.Mutex
	ops []escp.Operation
}

// NewDryRun returns a DryRun that has recorded nothing yet.
func NewDryRun() *DryRun { return &DryRun{} }

// Gate returns the gate to put in escp.Config: it records the operation and refuses it with
// ErrDryRun.
func (d *DryRun) Gate() escp.WriteGate {
	return func(_ context.Context, _ escp.InfoReader, op escp.Operation) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.ops = append(d.ops, op)
		return ErrDryRun
	}
}

// Operations returns what was recorded, in order. The result is a copy.
func (d *DryRun) Operations() []escp.Operation {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]escp.Operation, len(d.ops))
	copy(out, d.ops)
	return out
}
