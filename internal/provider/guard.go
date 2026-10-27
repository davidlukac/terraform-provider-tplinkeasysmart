package provider

import (
	"context"
	"errors"
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
func guardPorts(ctx context.Context, c portLister, allowDisruptive bool, named ...escp.PortID) error {
	if allowDisruptive {
		tflog.Warn(ctx, "allow_disruptive is set: the port-in-use guard is skipped", map[string]any{"ports": named})
		return nil
	}
	ports, err := escp.NewPortSet(named...)
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
	var used *safety.PortUseError
	if errors.As(err, &used) {
		return word(used)
	}
	return err
}

// allowDisruptiveAttribute is the schema of the opt-in that lets a resource
// change ports that carry traffic; description says what exactly it allows.
func allowDisruptiveAttribute(description string) schema.BoolAttribute {
	return schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: description}
}
