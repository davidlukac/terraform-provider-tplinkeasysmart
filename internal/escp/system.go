package escp

import (
	"context"
	"fmt"
)

// TLV types of the System page that are not part of discovery.
const (
	TLVPortCount = 10 // number of physical ports (1 byte)
	TLVAutoSave  = 13 // 1 = configuration changes are saved automatically (1 byte)
)

// System holds device facts that are not part of discovery.
type System struct {
	PortCount int
	AutoSave  bool
}

func parseSystem(ports, auto []TLV) (System, error) {
	var s System
	found := 0
	for _, t := range ports {
		if t.Type == TLVPortCount && len(t.Value) == 1 {
			s.PortCount, found = int(t.Value[0]), found+1
		}
	}
	for _, t := range auto {
		if t.Type == TLVAutoSave && len(t.Value) == 1 {
			s.AutoSave, found = t.Value[0] == 1, found+1
		}
	}
	if found != 2 {
		return s, fmt.Errorf("escp: incomplete system reply")
	}
	return s, nil
}

// System reads the port count and the auto-save flag.
func (c *Client) System(ctx context.Context) (System, error) {
	ports, err := c.getTLVs(ctx, TLVPortCount)
	if err != nil {
		return System{}, err
	}
	auto, err := c.getTLVs(ctx, TLVAutoSave)
	if err != nil {
		return System{}, err
	}
	return parseSystem(ports, auto)
}
