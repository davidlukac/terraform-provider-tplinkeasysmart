package escp

import (
	"bytes"
	"context"
	"fmt"
)

// ConfigMagic is the 4-byte prefix of the switch's configuration file.
var ConfigMagic = []byte{0x23, 0x89, 0x23, 0x89}

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
		req := []TLV{
			{TLVUsername, append([]byte(c.cfg.Username), 0)},
			{TLVPassword, append([]byte(c.cfg.Password), 0)},
			{TLVConfigFile, nil},
		}
		data, err := c.exchangeFragments(ctx, OpSet, req, TLVConfigFile)
		if err != nil {
			return err
		}
		out = data
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(out) <= len(ConfigMagic) || !bytes.HasPrefix(out, ConfigMagic) {
		return nil, fmt.Errorf("%w (%d bytes)", ErrInvalidConfig, len(out))
	}
	return out, nil
}
