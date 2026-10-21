package escp

import (
	"bytes"
	"context"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// configMagic is the 4-byte prefix of the switch's configuration file.
var configMagic = []byte{0x23, 0x89, 0x23, 0x89}

// ErrInvalidConfig means the reply was not a switch configuration file.
var ErrInvalidConfig = fmt.Errorf("escp: reply is not a switch configuration file")

// ConfigBackup downloads the switch's binary configuration file (the same
// bytes as System Tools > Backup Config in the web UI). The vendor utility
// requests it with an authenticated SET carrying an empty TLV 768; the file
// arrives in fragments, which are reassembled. Nothing on the switch changes.
func (c *Client) ConfigBackup(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	err := c.withSession(ctx, func() error {
		req := []wire.TLV{
			{Type: wire.TLVUsername, Value: append([]byte(c.cfg.Username), 0)},
			{Type: wire.TLVPassword, Value: append([]byte(c.cfg.Password), 0)},
			{Type: wire.TLVConfigFile, Value: nil},
		}
		data, err := c.exchangeFragments(ctx, wire.OpSet, req, wire.TLVConfigFile)
		if err != nil {
			return err
		}
		out = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) <= len(configMagic) || !bytes.HasPrefix(out, configMagic) {
		return nil, fmt.Errorf("%w (%d bytes)", ErrInvalidConfig, len(out))
	}
	return out, nil
}
