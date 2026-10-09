package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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

// importSingletonID accepts only the fixed id of a singleton resource and
// records it in state. It reports whether the id was valid.
func importSingletonID(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, id string) bool {
	if req.ID != id {
		resp.Diagnostics.AddError(summaryInvalidImportID, fmt.Sprintf("the only valid id is %q, got %q", id, req.ID))
		return false
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attrID), id)...)
	return true
}

// importPositiveInt accepts a positive integer id (port number, LAG group,
// VLAN id), records it under keyAttr, and reports whether it was valid. what
// describes the expected id in the error ("a port number").
func importPositiveInt(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, what, keyAttr string) bool {
	n, err := strconv.Atoi(req.ID)
	if err != nil || n < 1 {
		resp.Diagnostics.AddError(summaryInvalidImportID, fmt.Sprintf("expected %s, got %q", what, req.ID))
		return false
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(keyAttr), int64(n))...)
	return true
}

// resetAllowDisruptive sets the guard override to false for an imported
// resource: importing never opts in to disruptive changes.
func resetAllowDisruptive(ctx context.Context, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attrAllowDisruptive), false)...)
}
