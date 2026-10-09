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

// SpeedNames lists the configurable speeds in the order of the switch's codes 1..6.
var SpeedNames = []string{"auto", "10Mbps-half", "10Mbps-full", "100Mbps-half", "100Mbps-full", "1000Mbps-full"}

func speedCode(name string) (byte, error) {
	for i, n := range SpeedNames {
		if n == name {
			return byte(i + 1), nil
		}
	}
	return 0, fmt.Errorf("escp: unknown speed %q (valid: %v)", name, SpeedNames)
}

// SetPort writes the configuration of one port: admin state, speed and flow
// control (7-byte TLV 4096 as sent by the vendor utility). Changing a port that
// carries traffic can cut connectivity; callers must guard against that.
func (c *Client) SetPort(ctx context.Context, id int, enabled bool, speed string, flowControl bool) error {
	code, err := speedCode(speed)
	if err != nil {
		return err
	}
	if id < 1 || id > 32 {
		return fmt.Errorf("escp: invalid port %d", id)
	}
	return c.setTLVs(ctx, TLV{TLVPorts, []byte{byte(id), boolByte(enabled), 0, code, 0, boolByte(flowControl), 0}})
}
