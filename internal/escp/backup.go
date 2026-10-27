package escp

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// ErrInvalidConfig means the reply was not a switch configuration file.
var ErrInvalidConfig = errors.New("escp: reply is not a switch configuration file")

// ConfigBackup downloads the switch's binary configuration file (the same
// bytes as System Tools > Backup Config in the web UI). The vendor utility
// requests it with an authenticated SET carrying an empty TLV 768; the file
// arrives in fragments, which are reassembled. Nothing on the switch changes. Although it is
// a SET on the wire, it is a read: it never passes the write gate, so it works on any firmware
// (as the provider promises for reads) even where writes are refused.
func (c *Client) ConfigBackup(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	err := c.withSession(ctx, func() error {
		req := []wire.TLV{
			{Type: wire.TLVUsername, Value: append([]byte(c.cfg.Username), 0)},
			{Type: wire.TLVPassword, Value: append([]byte(c.cfg.Password.Reveal()), 0)},
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
	if len(out) <= len(wire.ConfigMagic) || !bytes.HasPrefix(out, wire.ConfigMagic) {
		return nil, fmt.Errorf("%w (%d bytes)", ErrInvalidConfig, len(out))
	}
	return out, nil
}
