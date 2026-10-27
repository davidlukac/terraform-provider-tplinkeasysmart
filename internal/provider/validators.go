package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// Plan-time validation. The rules live in the escp package (the same checks
// guard every write); these adapters surface them while Terraform plans, so a
// bad value is rejected before anything is applied. Unknown values are skipped
// by the framework and re-checked by the client at apply time.

// priorityRange is the web UI's QoS priority range, 1 (lowest) to 4 (highest).
const (
	minPriority = 1
	maxPriority = 4
)

// validPortNumber accepts a single port number.
func validPortNumber() validator.Int64 { return int64validator.Between(1, escp.MaxPort) }

// validPortSet accepts a set of port numbers.
func validPortSet() validator.Set {
	return setvalidator.ValueInt64sAre(int64validator.Between(1, escp.MaxPort))
}

// validByteID accepts an id that is one byte on the wire (LAG group, VLAN id).
func validByteID() validator.Int64 { return int64validator.Between(1, escp.MaxByteID) }

// speedNames are the configurable port speeds as Terraform spells them.
func speedNames() []string {
	var out []string
	for _, s := range escp.ConfigurableSpeeds() {
		out = append(out, s.String())
	}
	return out
}

// validSpeed accepts the speeds the switch can be configured to.
func validSpeed() validator.String { return stringvalidator.OneOf(speedNames()...) }

// validHostname accepts what the switch accepts as a device description.
func validHostname() []validator.String {
	return []validator.String{stringRule{
		description: "must be a device description: 1 to 32 letters, digits and hyphens, not starting or ending with a hyphen",
		summary:     "Invalid device description",
		check:       func(s string) error { return escp.Hostname(s).Validate() },
	}}
}

// qosModeNames lists the QoS mode names in mode-number order.
func qosModeNames() []string {
	var names []string
	for _, m := range escp.QoSModes() {
		names = append(names, m.String())
	}
	return names
}

func validQoSMode() validator.String { return stringvalidator.OneOf(qosModeNames()...) }

// validPriorities accepts a map from port number to priority 1-4.
func validPriorities() []validator.Map {
	return []validator.Map{
		mapvalidator.KeysAre(portKey{}),
		mapvalidator.ValueInt64sAre(int64validator.Between(minPriority, maxPriority)),
	}
}

// Limits as the descriptions state them, built from the client's constants so the text cannot
// drift from what is validated.
func rateRangeText() string {
	return fmt.Sprintf("0 (unlimited) or a multiple of %d up to %d", escp.RateStepKbps, escp.MaxBandwidthKbps)
}

// stormRateRangeText is the rate a storm control with traffic types needs.
func stormRateRangeText() string {
	return fmt.Sprintf("%d..%d kbps, multiple of %d", escp.RateStepKbps, escp.MaxBandwidthKbps, escp.RateStepKbps)
}

func vlanIDRangeText() string { return fmt.Sprintf("%d-%d", int(escp.MinVLANID), int(escp.MaxVLANID)) }

// rateKbps accepts 0 (unlimited) or a multiple of 64 up to the maximum.
type rateKbps struct{}

func (rateKbps) Description(context.Context) string {
	return rateRangeText() + " kbps"
}

func (v rateKbps) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (rateKbps) ValidateInt64(_ context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := escp.Kbps(req.ConfigValue.ValueInt64()).Validate(); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid rate", err.Error())
	}
}

// portKey accepts a map key that is a port number ("7").
type portKey struct{}

func (portKey) Description(context.Context) string {
	return fmt.Sprintf("a port number between 1 and %d", escp.MaxPort)
}

func (v portKey) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (portKey) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	n, err := strconv.Atoi(req.ConfigValue.ValueString())
	if err == nil {
		err = escp.PortID(n).Validate()
	}
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid port key",
			fmt.Sprintf("%q is not a port number between 1 and %d", req.ConfigValue.ValueString(), escp.MaxPort))
	}
}

// Cross-attribute rules, as pure functions of the configuration so they can be
// tested directly. Each returns a problem description, or "" when the
// configuration is fine or not yet fully known.

// stormProblem checks storm control: any selected traffic type needs a rate,
// and no type means no rate.
func stormProblem(unicast, multicast, broadcast types.Bool, rate types.Int64) string {
	for _, b := range []types.Bool{unicast, multicast, broadcast} {
		if b.IsUnknown() {
			return ""
		}
	}
	if rate.IsUnknown() {
		return ""
	}
	anyType := unicast.ValueBool() || multicast.ValueBool() || broadcast.ValueBool()
	switch {
	case anyType && rate.ValueInt64() < escp.RateStepKbps:
		return fmt.Sprintf("a traffic type is selected, so rate_kbps must be a multiple of %d between %d and %d", escp.RateStepKbps, escp.RateStepKbps, escp.MaxBandwidthKbps)
	case !anyType && rate.ValueInt64() != 0:
		return "rate_kbps is set but no traffic type (unknown_unicast, multicast, broadcast) is selected"
	}
	return ""
}

// mirrorProblem checks port mirroring: enabled needs a destination and at
// least one source and the destination cannot be a source; disabled must have
// no ports.
func mirrorProblem(enabled types.Bool, dest types.Int64, ingress, egress escp.PortSet) string {
	if enabled.IsUnknown() || dest.IsUnknown() {
		return ""
	}
	if !enabled.ValueBool() {
		if dest.ValueInt64() != 0 || !ingress.Union(egress).IsEmpty() {
			return "mirroring is disabled, so mirror_port, ingress_ports and egress_ports must be empty (the switch drops them when the mirror is off)"
		}
		return ""
	}
	if dest.ValueInt64() < 1 {
		return "mirroring is enabled, so mirror_port must be set to the destination port"
	}
	sources := ingress.Union(egress)
	if sources.IsEmpty() {
		return "mirroring is enabled, so at least one ingress or egress source port is needed"
	}
	if sources.Has(escp.PortID(dest.ValueInt64())) {
		return fmt.Sprintf("port %d cannot mirror itself", dest.ValueInt64())
	}
	return ""
}

// qosProblem checks that per-port priorities are only given in port-based
// mode, the only mode in which the switch honours them.
func qosProblem(mode types.String, priorities types.Map) string {
	if mode.IsUnknown() || priorities.IsUnknown() || len(priorities.Elements()) == 0 {
		return ""
	}
	if mode.ValueString() != escp.QoSPortBased.String() {
		return "per-port priorities only take effect when mode is port-based (the switch accepts them in the other modes but does not keep them); remove port_priorities or set mode = \"port-based\""
	}
	return ""
}

// validVLANID accepts an 802.1Q VLAN id or PVID.
func validVLANID() validator.Int64 {
	return int64validator.Between(int64(escp.MinVLANID), int64(escp.MaxVLANID))
}

// validVLANName accepts what the switch accepts as an 802.1Q VLAN name.
func validVLANName() []validator.String {
	return []validator.String{stringRule{
		description: "must be a VLAN name: up to 10 letters, digits, hyphens and underscores",
		summary:     "Invalid VLAN name",
		check:       func(s string) error { return escp.VLANName(s).Validate() },
	}}
}

// stringRule is a string validator whose rule is a value type's Validate (a MAC address, a device
// description, a VLAN name, a switch name), so the rule is written once, in the type, and not again
// as a pattern and a length in the schema.
type stringRule struct {
	description string
	summary     string
	check       func(string) error
}

func (r stringRule) Description(context.Context) string { return r.description }

func (r stringRule) MarkdownDescription(ctx context.Context) string { return r.Description(ctx) }

func (r stringRule) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := r.check(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, r.summary, err.Error())
	}
}

// validMAC accepts the text form of a switch MAC address, "aa:bb:cc:dd:ee:ff" (escp.ParseMAC says what
// is accepted). An empty string is refused: an argument that is set but empty is a mistake.
func validMAC() validator.String {
	return stringRule{
		description: "must be a MAC address such as aa:bb:cc:dd:ee:ff",
		summary:     "Invalid MAC address",
		check:       func(s string) error { _, err := escp.ParseMAC(s); return err },
	}
}
