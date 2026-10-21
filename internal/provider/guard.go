package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/safety"
)

// portLister is the part of the switch API the port guard needs.
type portLister interface {
	Ports(ctx context.Context) ([]escp.Port, error)
}

// guardPorts refuses to touch ports that carry a link or belong to a LAG unless
// the caller explicitly opted in. Writes to such ports can cut the management
// path or the uplink, and the switch has no console to recover from that.
func guardPorts(ctx context.Context, c portLister, allowDisruptive bool, ports ...int) error {
	if allowDisruptive {
		tflog.Warn(ctx, "allow_disruptive is set: the port-in-use guard is skipped", map[string]any{"ports": ports})
		return nil
	}
	all, err := c.Ports(ctx)
	if err != nil {
		return fmt.Errorf("cannot check whether the port is in use: %w", err)
	}
	for _, want := range ports {
		use := safety.UseOf(all, want)
		switch {
		case !use.Exists:
			return fmt.Errorf("port %d does not exist on this switch", want)
		case use.InLAG():
			return fmt.Errorf("port %d is a member of LAG%d; set allow_disruptive = true to change it anyway", want, use.LAG)
		case use.LinkUp():
			return fmt.Errorf("port %d has a link up (%s) and may carry traffic or management access; set allow_disruptive = true to change it anyway", want, use.Speed)
		}
	}
	return nil
}

// allowDisruptiveAttribute is the schema of the opt-in that lets a resource
// change ports that carry traffic; description says what exactly it allows.
func allowDisruptiveAttribute(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: description}
}
