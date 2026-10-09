package escp

import (
	"context"
	"fmt"
)

// TLV types of the settings read by this package (see docs/escp-protocol.md).
const (
	TLVPorts = 4096
)

// Port is one row of the Port Setting page.
type Port struct {
	ID                    int
	Enabled               bool
	LAG                   int    // 0 = not in a LAG
	ConfiguredSpeed       string // auto, 10Mbps-half, ... 1000Mbps-full
	ActualSpeed           string // link-down or a negotiated speed
	ConfiguredFlowControl bool
	ActualFlowControl     bool
}

var speedNames = map[byte]string{
	0: "link-down", 1: "auto", 2: "10Mbps-half", 3: "10Mbps-full",
	4: "100Mbps-half", 5: "100Mbps-full", 6: "1000Mbps-full",
}

func speedName(b byte) string {
	if n, ok := speedNames[b]; ok {
		return n
	}
	return fmt.Sprintf("unknown(%d)", b)
}

// parsePorts decodes TLV 4096 values: port, state, lag, cfg speed, actual
// speed, cfg flow control, actual flow control.
func parsePorts(tlvs []TLV) ([]Port, error) {
	var ports []Port
	for _, t := range tlvs {
		if t.Type != TLVPorts {
			continue
		}
		v := t.Value
		if len(v) < 7 {
			return nil, fmt.Errorf("escp: port TLV too short (%d bytes)", len(v))
		}
		ports = append(ports, Port{
			ID: int(v[0]), Enabled: v[1] == 1, LAG: int(v[2]),
			ConfiguredSpeed: speedName(v[3]), ActualSpeed: speedName(v[4]),
			ConfiguredFlowControl: v[5] == 1, ActualFlowControl: v[6] == 1,
		})
	}
	return ports, nil
}

// Ports reads the Port Setting page: state, LAG membership, speed and flow control per port.
func (c *Client) Ports(ctx context.Context) ([]Port, error) {
	tlvs, err := c.getTLVs(ctx, TLVPorts)
	if err != nil {
		return nil, err
	}
	return parsePorts(tlvs)
}
