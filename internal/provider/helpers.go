package provider

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/davidlukac/escp-go"
)

// Attribute names and diagnostic summaries shared by several resources.
const (
	attrID              = "id"
	attrAllowDisruptive = "allow_disruptive"

	summaryInvalidImportID = "Invalid import id"
)

// clientFrom extracts the narrow client interface T a resource or data source
// needs from the provider data (the zero value before the provider is
// configured). Every consumer asks for its own interface, never the whole
// switch API.
func clientFrom[T any](data any, diags *diag.Diagnostics) T {
	var zero T
	if data == nil {
		return zero
	}
	c, ok := data.(T)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", data))
		return zero
	}
	return c
}

// applyPlan is the shared body of Create and Update: decode the plan, apply it
// to the switch, and store the resulting (read-back) state. apply reports
// problems through its add function and returns false to leave the state untouched.
func applyPlan[T any](ctx context.Context, plan tfsdk.Plan, state *tfsdk.State, diags *diag.Diagnostics,
	apply func(ctx context.Context, m *T, add func(summary, detail string)) bool) {
	var m T
	diags.Append(plan.Get(ctx, &m)...)
	if diags.HasError() {
		return
	}
	if apply(ctx, &m, diags.AddError) {
		diags.Append(state.Set(ctx, &m)...)
	}
}

// applyUpdate is the shared body of Update for the resources behind a guard. When the plan differs
// from the prior state in allow_disruptive alone, nothing is guarded and nothing is written: the
// flag is an instruction about the next change, not a setting of the switch, so adding it must
// not rewrite a live port or LAG, and removing it must not be refused by a guard that has nothing
// to guard. Computed attributes the plan leaves unknown keep their prior values. Any other
// difference goes through applyPlan, guard included.
func applyUpdate[T any](ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse,
	apply func(ctx context.Context, m *T, add func(summary, detail string)) bool) {
	if merged, ok := planWithOnlyTheFlagChanged(req.Plan.Raw, req.State.Raw); ok {
		resp.State.Raw = merged
		return
	}
	applyPlan(ctx, req.Plan, &resp.State, &resp.Diagnostics, apply)
}

// planWithOnlyTheFlagChanged returns the planned state, with the prior value in place of every
// unknown one, when plan and prior state agree on every attribute except allow_disruptive. Any
// other difference (including a value the plan cannot decide yet) reports false.
func planWithOnlyTheFlagChanged(plan, prior tftypes.Value) (tftypes.Value, bool) {
	var p, s map[string]tftypes.Value
	if plan.As(&p) != nil || prior.As(&s) != nil || p == nil || s == nil {
		return tftypes.Value{}, false
	}
	merged := make(map[string]tftypes.Value, len(p))
	for name, pv := range p {
		sv, ok := s[name]
		switch {
		case !ok:
			return tftypes.Value{}, false
		case name == attrAllowDisruptive:
			merged[name] = pv
		case !pv.IsKnown():
			merged[name] = sv
		case pv.Equal(sv):
			merged[name] = pv
		default:
			return tftypes.Value{}, false
		}
	}
	return tftypes.NewValue(plan.Type(), merged), true
}

// importSingletonID accepts the fixed id of a singleton resource, optionally
// prefixed with a switch name ("sw02:led"), and records id and switch in state.
// It reports whether the id was valid.
func importSingletonID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, id string) bool {
	sw, rest := splitQualified(req.ID)
	if rest != id {
		resp.Diagnostics.AddError(summaryInvalidImportID, fmt.Sprintf("the only valid id is %q (optionally prefixed with a switch name and %q), got %q", id, idSeparator, req.ID))
		return false
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attrID), req.ID)...)
	importSwitch(ctx, resp, sw)
	return true
}

// importPositiveInt accepts a positive integer id (port number, LAG group,
// VLAN id), optionally prefixed with a switch name ("sw02:7"), records it
// under keyAttr and the switch under `switch`, and reports whether it was
// valid. what describes the expected id in the error ("a port number").
func importPositiveInt(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, what, keyAttr string) bool {
	sw, rest := splitQualified(req.ID)
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError(summaryInvalidImportID, fmt.Sprintf("expected %s (optionally prefixed with a switch name and %q), got %q", what, idSeparator, req.ID))
		return false
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(keyAttr), int64(n))...)
	importSwitch(ctx, resp, sw)
	return true
}

// importSwitch records the switch named in an import id, if any.
func importSwitch(ctx context.Context, resp *resource.ImportStateResponse, sw switchName) {
	if sw != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attrSwitch), string(sw))...)
	}
}

// resetAllowDisruptive sets the guard override to false for an imported
// resource: importing never opts in to disruptive changes.
func resetAllowDisruptive(ctx context.Context, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attrAllowDisruptive), false)...)
}

// defaultFalse and defaultZero read an optional attribute from the raw
// configuration: an omitted attribute is null there and takes its schema
// default (false / 0) at plan time. Unknown stays unknown.
func defaultFalse(b types.Bool) types.Bool {
	if b.IsNull() {
		return types.BoolValue(false)
	}
	return b
}

func defaultZero(n types.Int64) types.Int64 {
	if n.IsNull() {
		return types.Int64Value(0)
	}
	return n
}

// portsOf reads a configured port set; ok is false while it is not known yet.
// An omitted set is null and means "no ports".
func portsOf(ctx context.Context, s types.Set) (ports escp.PortSet, ok bool) {
	if s.IsUnknown() {
		return 0, false
	}
	if s.IsNull() {
		return 0, true
	}
	ports, err := setToPorts(ctx, s)
	return ports, err == nil
}

// setToPorts reads a Terraform set of port numbers; a number outside 1 to escp.MaxPort is an error.
func setToPorts(ctx context.Context, s types.Set) (escp.PortSet, error) {
	var vals []int64
	if d := s.ElementsAs(ctx, &vals, false); d.HasError() {
		return 0, fmt.Errorf("%v", d)
	}
	ports := make([]escp.PortID, 0, len(vals))
	for _, v := range vals {
		ports = append(ports, escp.PortID(v))
	}
	return escp.NewPortSet(ports...)
}

// portsToSet is the Terraform set of a port set's numbers.
func portsToSet(s escp.PortSet) types.Set {
	vals := make([]attr.Value, 0, s.Len())
	for _, p := range s.Ports() {
		vals = append(vals, types.Int64Value(int64(p)))
	}
	return types.SetValueMust(types.Int64Type, vals)
}

// portInt64s lists a port set's numbers in ascending order, for the data sources' list attributes.
func portInt64s(s escp.PortSet) []int64 {
	out := make([]int64, 0, s.Len())
	for _, p := range s.Ports() {
		out = append(out, int64(p))
	}
	return out
}

// inactiveModeError means a VLAN setting cannot be read or written because the VLAN mode it
// belongs to is not the active one. The message is the refusal a write gets; a read or a destroy
// turns it into a warning, because failing there would break the refresh of every other
// resource in the configuration whenever someone changes the mode outside Terraform.
type inactiveModeError struct{ msg string }

func (e *inactiveModeError) Error() string { return e.msg }

// warnInactiveMode adds a warning and reports true when err is an inactiveModeError; what names
// the settings that cannot be read and effect says what the provider does instead.
func warnInactiveMode(diags *diag.Diagnostics, err error, what, effect string) bool {
	var inactive *inactiveModeError
	if !errors.As(err, &inactive) {
		return false
	}
	diags.AddWarning("The VLAN mode of "+what+" is not active", inactive.msg+". "+effect)
	return true
}

// addrString is the text of an address the switch reported: empty when the reply carried none.
func addrString(a netip.Addr) string {
	if !a.IsValid() {
		return ""
	}
	return a.String()
}
