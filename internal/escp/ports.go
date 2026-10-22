package escp

import (
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// Layout of a port entry (TLV 4096): one byte per field.
const (
	portEntryLen    = 7
	portOffPort     = 0
	portOffState    = 1
	portOffLAG      = 2
	portOffCfgSpeed = 3
	portOffActSpeed = 4
	portOffCfgFlow  = 5
	portOffActFlow  = 6
)

// Port is one row of the Port Setting page.
type Port struct {
	ID                    int
	Enabled               bool
	LAG                   int   // 0 = not in a LAG
	ConfiguredSpeed       Speed // auto, 10Mbps-half, ... 1000Mbps-full
	ActualSpeed           Speed // SpeedLinkDown or a negotiated speed
	ConfiguredFlowControl bool
	ActualFlowControl     bool
}

// Speed is a port's speed and duplex, spelled as the web UI and the Terraform
// provider spell it. The values are part of the provider's state: do not change them.
type Speed string

// The speeds a port can be configured to (codes 1..6 on the wire), and the one state
// that is only ever reported.
const (
	SpeedAuto     Speed = "auto"
	Speed10Half   Speed = "10Mbps-half"
	Speed10Full   Speed = "10Mbps-full"
	Speed100Half  Speed = "100Mbps-half"
	Speed100Full  Speed = "100Mbps-full"
	Speed1000Full Speed = "1000Mbps-full"
	// SpeedLinkDown is the ActualSpeed of a port with no link; it cannot be configured.
	SpeedLinkDown Speed = "link-down"
)

// configurableSpeeds are the speeds SetPort accepts, in the order of the switch's codes 1..6.
var configurableSpeeds = [...]Speed{SpeedAuto, Speed10Half, Speed10Full, Speed100Half, Speed100Full, Speed1000Full}

// ConfigurableSpeeds lists the speeds SetPort accepts, in the order of the switch's codes 1..6.
func ConfigurableSpeeds() []Speed { return append([]Speed(nil), configurableSpeeds[:]...) }

// String returns the speed as spelled in the web UI.
func (s Speed) String() string { return string(s) }

// AllowsFlowControl reports whether flow control can be switched on at this speed. The switch
// keeps it only with auto-negotiation or at 1000Mbps-full; at a fixed 10 or 100 Mbps speed it
// accepts the write but stores flow control as off (measured on firmware 20250710, every
// fixed speed below 1000 Mbps). The vendor utility enforces the same rule: its flow control
// choice is forced to off and disabled unless the speed is auto or 1000 Mbps full, with the
// message "Failed to enable flow control when speed in Force mode".
func (s Speed) AllowsFlowControl() bool { return s == SpeedAuto || s == Speed1000Full }

// ParseSpeed converts a configurable speed name; SpeedLinkDown and unknown names are errors.
func ParseSpeed(name string) (Speed, error) {
	for _, s := range configurableSpeeds {
		if string(s) == name {
			return s, nil
		}
	}
	return "", fmt.Errorf("escp: unknown speed %q (valid: %v)", name, configurableSpeeds)
}

// speedFromCode names a speed code of a read reply (0 = no link).
func speedFromCode(b byte) Speed {
	if b == 0 {
		return SpeedLinkDown
	}
	if int(b) <= len(configurableSpeeds) {
		return configurableSpeeds[b-1]
	}
	return Speed(fmt.Sprintf("unknown(%d)", b))
}

// code returns the wire code of a configurable speed.
func (s Speed) code() (byte, error) {
	for i, n := range configurableSpeeds {
		if n == s {
			return byte(i + 1), nil
		}
	}
	return 0, fmt.Errorf("escp: unknown speed %q (valid: %v)", string(s), configurableSpeeds)
}

// parsePorts decodes TLV 4096 values: port, state, lag, cfg speed, actual
// speed, cfg flow control, actual flow control.
func parsePorts(tlvs []wire.TLV) ([]Port, error) {
	var ports []Port
	for _, t := range tlvs {
		if t.Type != wire.TLVPorts {
			continue
		}
		v := t.Value
		if len(v) < portEntryLen {
			return nil, fmt.Errorf("escp: port TLV too short (%d bytes)", len(v))
		}
		ports = append(ports, Port{
			ID: int(v[portOffPort]), Enabled: v[portOffState] == 1, LAG: int(v[portOffLAG]),
			ConfiguredSpeed: speedFromCode(v[portOffCfgSpeed]), ActualSpeed: speedFromCode(v[portOffActSpeed]),
			ConfiguredFlowControl: v[portOffCfgFlow] == 1, ActualFlowControl: v[portOffActFlow] == 1,
		})
	}
	return ports, nil
}

// Ports reads the Port Setting page: state, LAG membership, speed and flow control per port.
func (c *Client) Ports(ctx context.Context) ([]Port, error) {
	tlvs, err := c.getTLVs(ctx, wire.TLVPorts)
	if err != nil {
		return nil, err
	}
	return parsePorts(tlvs)
}

// PortConfig is what SetPort writes to one port. All fields are required: the switch
// takes the whole entry, so a caller changing one field passes the others as read.
// FlowControl can only be true with a speed for which Speed.AllowsFlowControl is true.
type PortConfig struct {
	ID          int
	Enabled     bool
	Speed       Speed // a configurable speed; SpeedLinkDown is refused
	FlowControl bool
}

// SetPort writes the configuration of one port: admin state, speed and flow
// control (7-byte TLV 4096 as sent by the vendor utility). Flow control can only be
// on with speed auto or 1000Mbps-full (see Speed.AllowsFlowControl): any other
// combination is refused before anything is sent, because the switch would accept
// it and silently keep flow control off. Changing a port that carries traffic can
// cut connectivity; callers must guard against that.
//
// For a port that is a member of a LAG the switch refuses half duplex (the error names
// that), but it accepts a different speed, flow control or admin state and so lets
// the members drift apart: keep them alike, or change the whole group.
func (c *Client) SetPort(ctx context.Context, cfg PortConfig) error {
	code, err := cfg.Speed.code()
	if err != nil {
		return err
	}
	if err := ValidatePort(cfg.ID); err != nil {
		return err
	}
	if cfg.FlowControl && !cfg.Speed.AllowsFlowControl() {
		return fmt.Errorf("escp: port %d: flow control needs speed %s or %s, not %s (the switch keeps it off at a fixed 10 or 100 Mbps speed)", cfg.ID, SpeedAuto, Speed1000Full, cfg.Speed)
	}
	entry := make([]byte, portEntryLen)
	entry[portOffPort], entry[portOffState], entry[portOffCfgSpeed], entry[portOffCfgFlow] = byte(cfg.ID), boolByte(cfg.Enabled), code, boolByte(cfg.FlowControl)
	return nameRefusal(c.setTLVs(ctx, wire.TLV{Type: wire.TLVPorts, Value: entry}), portRefusals)
}

// portRefusals names the refusals of a port write that were provoked on a real switch
// (firmware 20250710): half duplex on a port that is a member of a LAG. Half duplex on a
// port that is not a member is accepted.
var portRefusals = map[int32]string{
	lagRefusedHalfDuplex: "the port is a member of a LAG, and LAG members must be full duplex",
}
