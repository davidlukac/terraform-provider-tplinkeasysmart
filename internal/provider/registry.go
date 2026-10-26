package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Several switches can be managed by one provider configuration: the provider
// holds a registry of named switches, and every resource and data source picks
// one with its optional `switch` argument. Static provider aliases keep
// working: an alias is simply a provider whose registry has only its default
// switch.

const (
	attrSwitch = "switch"

	summaryUnknownSwitch = "Cannot select the switch"
)

// switchSource hands out the client of a switch by name.
type switchSource interface {
	// Client returns the client for the named switch. An empty name selects the
	// default switch.
	Client(name switchName) (SwitchClient, error)
}

// switchRegistry is the switchSource built from the provider configuration.
type switchRegistry struct {
	def   SwitchClient            // the provider's own host/username/password, if configured
	named map[string]SwitchClient // the `switches` map
}

func (r *switchRegistry) names() string {
	names := make([]string, 0, len(r.named))
	for n := range r.named {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// Client implements switchSource. Without a name the default switch is used;
// if there is none, the only configured switch; otherwise the choice is
// ambiguous and refused.
func (r *switchRegistry) Client(name switchName) (SwitchClient, error) {
	if name != "" {
		c, ok := r.named[string(name)]
		if !ok {
			return nil, fmt.Errorf("unknown switch %q (configured: %s)", name, r.names())
		}
		return c, nil
	}
	if r.def != nil {
		return r.def, nil
	}
	if len(r.named) == 1 {
		for _, c := range r.named {
			return c, nil
		}
	}
	return nil, fmt.Errorf("several switches are configured and none is the default: set `switch` to one of %s", r.names())
}

// sourceFrom extracts the switch source from provider data (nil before the
// provider is configured).
func sourceFrom(data any, diags *diag.Diagnostics) switchSource {
	if data == nil {
		return nil
	}
	s, ok := data.(switchSource)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("got %T", data))
		return nil
	}
	return s
}

// clientFor resolves the client of the named switch (null selects the
// default) as the narrow interface C the caller needs.
func clientFor[C any](src switchSource, name types.String) (C, error) {
	var zero C
	if src == nil {
		return zero, errors.New("the provider is not configured")
	}
	c, err := src.Client(switchNameOf(name))
	if err != nil {
		return zero, err
	}
	narrow, ok := c.(C)
	if !ok {
		return zero, fmt.Errorf("the client %T does not provide %T", c, zero)
	}
	return narrow, nil
}

// bindSwitch returns a copy of the consumer r wired to the client of the named
// switch (bind installs the client and the switch name), or reports why that
// is impossible through add and returns nil.
func bindSwitch[R, C any](add func(summary, detail string), src switchSource, name types.String, r *R, bind func(*R, C, types.String)) *R {
	c, err := clientFor[C](src, name)
	if err != nil {
		add(summaryUnknownSwitch, err.Error())
		return nil
	}
	cp := *r
	bind(&cp, c, name)
	return &cp
}

// attrGetter is implemented by the framework's State, Plan and Config.
type attrGetter interface {
	GetAttribute(ctx context.Context, p path.Path, target any) diag.Diagnostics
}

// switchOf reads the `switch` argument (null when omitted).
func switchOf(ctx context.Context, g attrGetter, diags *diag.Diagnostics) types.String {
	var sw types.String
	diags.Append(g.GetAttribute(ctx, path.Root(attrSwitch), &sw)...)
	return sw
}

// qualifiedID is the id of an object on the switch the `switch` argument names (see switchName.qualify).
func qualifiedID(sw types.String, id string) types.String {
	return types.StringValue(switchNameOf(sw).qualify(id))
}

const switchArgDescription = "Name of the switch (a key of the provider's `switches` map) this object lives on. " +
	"Omit it to use the provider's own switch (`host`), or the only configured switch."

// resourceSwitchAttribute is the `switch` argument of every resource. A
// different switch is a different device, so changing it replaces the resource.
func resourceSwitchAttribute() rschema.StringAttribute {
	return rschema.StringAttribute{
		Optional:      true,
		Description:   switchArgDescription + " Changing it replaces the resource.",
		Validators:    validSwitchName(),
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

// dataSourceSwitchAttribute is the `switch` argument of every data source.
func dataSourceSwitchAttribute() dschema.StringAttribute {
	return dschema.StringAttribute{Optional: true, Description: switchArgDescription, Validators: validSwitchName()}
}

// checkSwitch reports, while planning, a `switch` that cannot be resolved,
// so a typo or an ambiguous configuration fails before anything is applied.
func checkSwitch(ctx context.Context, src switchSource, plan tfsdk.Plan, diags *diag.Diagnostics) {
	if src == nil || plan.Raw.IsNull() { // provider not configured yet, or a destroy plan
		return
	}
	sw := switchOf(ctx, plan, diags)
	if sw.IsUnknown() {
		return
	}
	if _, err := src.Client(switchNameOf(sw)); err != nil {
		diags.AddAttributeError(path.Root(attrSwitch), summaryUnknownSwitch, err.Error())
	}
}

// keepSwitch writes the `switch` argument back into state after a read that
// built its result from scratch, so the argument is never lost between runs.
func keepSwitch(ctx context.Context, sw types.String, state *tfsdk.State, diags *diag.Diagnostics) {
	if diags.HasError() || state.Raw.IsNull() { // failed, or the object was removed from state
		return
	}
	diags.Append(state.SetAttribute(ctx, path.Root(attrSwitch), sw)...)
}
