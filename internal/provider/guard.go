package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/davidlukac/escp-go/model"
	"github.com/davidlukac/escp-go/safety"
)

// portLister is the part of the switch API the port guard and the ports data source need.
type portLister interface {
	Ports(ctx context.Context) ([]model.Port, error)
}

// guardPorts refuses to touch ports that carry a link or belong to a LAG unless
// the caller explicitly opted in. Writes to such ports can cut the management
// path or the uplink, and the switch has no console to recover from that.
func guardPorts(ctx context.Context, c portLister, allowDisruptive bool, named ...model.PortID) error {
	if allowDisruptive {
		tflog.Warn(ctx, "allow_disruptive is set: the port-in-use guard is skipped", map[string]any{"ports": named})
		return nil
	}
	ports, err := model.NewPortSet(named...)
	if err != nil { // a port number no switch has is not "free": refuse, as for a port the switch does not list
		return err
	}
	all, err := c.Ports(ctx)
	if err != nil {
		return fmt.Errorf("cannot check whether the port is in use: %w", err)
	}
	return wordPortRefusal(safety.CheckPorts(all, safety.PortRule{}, ports), func(used *safety.PortUseError) error {
		switch used.Reason {
		case safety.PortMissing:
			return errors.New(used.Error())
		case safety.PortLinkUp:
			return fmt.Errorf("port %d has a link up (%s) and may carry traffic or management access; set allow_disruptive = true to change it anyway", used.Port, used.Speed)
		default:
			return fmt.Errorf("port %d is a member of LAG%d; set allow_disruptive = true to change it anyway", used.Port, used.LAG)
		}
	})
}

// wordPortRefusal turns the refusal of safety.CheckPorts into the sentence of a guard. Anything
// that is not a *safety.PortUseError is returned as it is, so a refusal of a kind this code does
// not know can never read as "the ports are free" and let a write through.
func wordPortRefusal(err error, word func(used *safety.PortUseError) error) error {
	if err == nil {
		return nil
	}
	if used, ok := errors.AsType[*safety.PortUseError](err); ok {
		return word(used)
	}
	return err
}

// allowDisruptiveAttribute is the schema of the opt-in that lets a resource
// change ports that carry traffic; description says what exactly it allows.
func allowDisruptiveAttribute(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: description}
}

// ingressStormNote is the paragraph both limit resources carry about the switch's one limiter per port for
// incoming traffic: it serves an ingress limit or storm control, never both, and the client refuses the second.
// other is the setting that must be off, otherResource the resource that manages it, and trigger what is refused
// while the other is on.
func ingressStormNote(other, otherResource, trigger string) string {
	return "**One limiter per port:** the switch keeps either an ingress limit or storm control on a port (writing one turns the other off without a word), " +
		"so " + trigger + " is refused while " + other + " is on for the port, before anything is written. " +
		"To change over, turn " + other + " off with " + otherResource + " first and make this resource `depends_on` it, so that the applies run in that order " +
		"(without an order, one of the two can be refused). The egress limit is independent of both. Verified on firmware 20230218 and 20250710."
}

// standingOptIn is what a destroy plan needs to know about an object whose delete acts on the
// switch: where it lives, whether the allow_disruptive stored in its state is on, which ports it
// involves and which LAG is its own (zero for none).
type standingOptIn struct {
	sw    types.String
	allow types.Bool
	ports model.PortSet
	own   model.LAGID
}

// destroyPlanCheck is what warnOnDestroyPlan needs of a resource: where its switches come from,
// what to call it in the warning and how to read standingOptIn from its state.
type destroyPlanCheck[T any] struct {
	src      switchSource
	what     string
	standing func(ctx context.Context, state *T) standingOptIn
}

// warnOnDestroyPlan adds a warning to a destroy plan of an object that acts on the switch when
// the allow_disruptive kept in its state lets the delete go ahead on a port that has a link up or
// is in a LAG: the guard that would have refused it is switched off by a flag that outlived the
// configuration that set it. It is a warning only (a destroy that is meant is not blocked;
// lifecycle { prevent_destroy = true } blocks it), and best effort: a switch that cannot be read
// at plan time is the apply's business.
func warnOnDestroyPlan[T any](ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse, check destroyPlanCheck[T]) {
	src, what, standing := check.src, check.what, check.standing
	if !req.Plan.Raw.IsNull() || req.State.Raw.IsNull() || src == nil { // not a destroy plan
		return
	}
	var state T
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s := standing(ctx, &state)
	if !s.allow.ValueBool() || s.ports.IsEmpty() {
		return
	}
	c, err := clientFor[portLister](src, s.sw)
	if err != nil {
		return
	}
	all, err := c.Ports(ctx)
	if err != nil {
		return
	}
	var used *safety.PortUseError
	if !errors.As(safety.CheckPorts(all, safety.PortRule{TolerateLAG: s.own}, s.ports), &used) {
		return
	}
	switch used.Reason {
	case safety.PortLinkUp:
		resp.Diagnostics.AddWarning("Destroying the "+what+" on a port that has a link up",
			fmt.Sprintf("port %d has a link up (%s) and this %s has allow_disruptive = true in its state, so the guard that refuses such a change is off and the destroy will go ahead. "+
				"Set lifecycle { prevent_destroy = true } to block it, or apply allow_disruptive = false first to have the guard check the destroy.", used.Port, used.Speed, what))
	case safety.PortMissing:
		// a port the switch does not have is not in use: nothing to warn about
	case safety.PortInLAG:
		resp.Diagnostics.AddWarning("Destroying the "+what+" on a port that is a LAG member",
			fmt.Sprintf("port %d is a member of LAG%d and this %s has allow_disruptive = true in its state, so the guard that refuses such a change is off and the destroy will go ahead. "+
				"Set lifecycle { prevent_destroy = true } to block it, or apply allow_disruptive = false first to have the guard check the destroy.", used.Port, used.LAG, what))
	}
}
