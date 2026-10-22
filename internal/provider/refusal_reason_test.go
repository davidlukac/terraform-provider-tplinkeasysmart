package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// refusingClient answers a LAG or port write the way the client does when the switch refuses it
// and the cause is known: a *escp.RejectedError that carries the reason.
type refusingClient struct {
	SwitchClient
	lag, port error
}

func (r *refusingClient) SetLAG(context.Context, int, []int) error { return r.lag }
func (r *refusingClient) SetPort(context.Context, escp.PortConfig) error {
	return r.port
}

// The user has to see why the switch refused, not only that it did.
func TestAccTheReasonOfARefusedWriteReachesTheUser(t *testing.T) {
	t.Run("LAG members with different speeds", func(t *testing.T) {
		c := &refusingClient{SwitchClient: pvFakes(), lag: &escp.RejectedError{Code: 7406, Reason: "the member ports do not have the same speed"}}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(c),
			Steps: []resource.TestStep{{
				Config:      lagConfig(2, "  ports = [6, 7]\n"),
				ExpectError: regexp.MustCompile(`(?s)Setting LAG failed.*error\s+code\s+7406.*the\s+member\s+ports\s+do\s+not\s+have\s+the\s+same\s+speed`),
			}},
		})
	})
	t.Run("half duplex on a LAG member", func(t *testing.T) {
		c := &refusingClient{SwitchClient: pvFakes(), port: &escp.RejectedError{Code: 7407, Reason: "the port is a member of a LAG, and LAG members must be full duplex"}}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: factories(c),
			Steps: []resource.TestStep{{
				Config:      portConfig(1, "  speed = \"100Mbps-half\"\n  allow_disruptive = true\n"),
				ExpectError: regexp.MustCompile(`(?s)Setting\s+port\s+failed.*error\s+code\s+7407.*member\s+of\s+a\s+LAG.*full\s+duplex`),
			}},
		})
	})
}
