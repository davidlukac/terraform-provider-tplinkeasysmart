package regression

import (
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// CheckTestPorts proves, before any write, that every test port of the target exists, has no
// link and is in no LAG. The configuration of a target says which ports are free; the switch is
// asked, because a cable plugged in since then must stop the run, not be written to.
func CheckTestPorts(ctx context.Context, c *escp.Client, t Target) error {
	ports, err := c.Ports(ctx)
	if err != nil {
		return fmt.Errorf("cannot read the ports to check the test ports: %w", err)
	}
	byID := map[int]escp.Port{}
	for _, p := range ports {
		byID[p.ID] = p
	}
	for _, id := range t.TestPorts {
		p, ok := byID[id]
		switch {
		case !ok:
			return fmt.Errorf("test port %d does not exist on the switch", id)
		case p.ActualSpeed != escp.SpeedLinkDown:
			return fmt.Errorf("test port %d has a link (%s); free it or choose another test port", id, p.ActualSpeed)
		case p.LAG != 0:
			return fmt.Errorf("test port %d is a member of LAG %d", id, p.LAG)
		}
	}
	return nil
}
