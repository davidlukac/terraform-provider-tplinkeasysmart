package provider

import (
	"context"
	"fmt"
)

// guardPorts refuses to touch ports that carry a link or belong to a LAG unless
// the caller explicitly opted in. Writes to such ports can cut the management
// path or the uplink, and the switch has no console to recover from that.
func guardPorts(ctx context.Context, c SwitchClient, allowDisruptive bool, ports ...int) error {
	if allowDisruptive {
		return nil
	}
	all, err := c.Ports(ctx)
	if err != nil {
		return fmt.Errorf("cannot check whether the port is in use: %w", err)
	}
	for _, want := range ports {
		found := false
		for _, p := range all {
			if p.ID != want {
				continue
			}
			found = true
			if p.LAG != 0 {
				return fmt.Errorf("port %d is a member of LAG%d; set allow_disruptive = true to change it anyway", want, p.LAG)
			}
			if p.ActualSpeed != "link-down" {
				return fmt.Errorf("port %d has a link up (%s) and may carry traffic or management access; set allow_disruptive = true to change it anyway", want, p.ActualSpeed)
			}
		}
		if !found {
			return fmt.Errorf("port %d does not exist on this switch", want)
		}
	}
	return nil
}
