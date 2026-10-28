package escp

import "context"

// A port has one limiter for incoming traffic, and the switch lends it to either the ingress bandwidth limit
// or storm control: writing one turns the other off on that port, with a normal reply and no refusal
// (probed on firmware 20250710 and 20230218: an ingress limit other than 0 clears storm control, storm control with a
// traffic type selected clears the ingress limit; an ingress limit of 0, storm control with no type and the
// egress limit leave the other alone). A write would therefore succeed, and the setting it silently removed
// would be found missing by the next read. The client reads the other setting first and refuses, so the
// caller decides what stays.

// requireNoStorm refuses an ingress limit on a port whose storm control is on.
func (c *Client) requireNoStorm(ctx context.Context, port PortID, ingress Kbps) error {
	rows, err := c.StormControl(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Port == port && r.Enabled {
			return invalidf("escp: port %d has storm control on (%s), and the switch keeps either storm control or an ingress limit on a port: "+
				"an ingress limit of %s would turn storm control off. Turn storm control off first, or leave the ingress limit at 0", port, r.RateKbps, ingress)
		}
	}
	return nil
}

// requireNoIngressLimit refuses storm control on a port that has an ingress limit.
func (c *Client) requireNoIngressLimit(ctx context.Context, port PortID) error {
	rows, err := c.Bandwidth(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Port == port && r.IngressKbps != 0 {
			return invalidf("escp: port %d has an ingress limit of %s, and the switch keeps either storm control or an ingress limit on a port: "+
				"storm control would remove the limit. Set the ingress limit to 0 first, or leave storm control off", port, r.IngressKbps)
		}
	}
	return nil
}
