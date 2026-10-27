package escp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// ErrInvalidConfig means the reply was not a switch configuration file.
var ErrInvalidConfig = errors.New("escp: reply is not a switch configuration file")

// ConfigFile is the switch's binary configuration file, as ConfigBackup returns it. It holds
// the account block of the switch (obfuscated, not safe), so it prints as its size and hash and
// never as its bytes, under every fmt verb and in slog; convert it to []byte for the bytes.
type ConfigFile []byte

// Size is the length of the file in bytes.
func (f ConfigFile) Size() int { return len(f) }

// SHA256 is the hex SHA-256 of the file: the hash that tells whether the configuration changed.
func (f ConfigFile) SHA256() string {
	sum := sha256.Sum256(f)
	return hex.EncodeToString(sum[:])
}

// Base64 is the file in standard base64, for a place that can only hold text.
func (f ConfigFile) Base64() string { return base64.StdEncoding.EncodeToString(f) }

// String describes the file by its size and hash, not by its content.
func (f ConfigFile) String() string {
	return fmt.Sprintf("configuration file (%d bytes, sha256 %s)", f.Size(), f.SHA256())
}

// GoString is String, so %#v does not print the bytes either.
func (f ConfigFile) GoString() string { return f.String() }

// LogValue makes slog log the size and hash instead of the bytes.
func (f ConfigFile) LogValue() slog.Value { return slog.StringValue(f.String()) }

// ConfigBackup downloads the switch's binary configuration file (the same
// bytes as System Tools > Backup Config in the web UI). The vendor utility
// requests it with an authenticated SET carrying an empty TLV 768; the file
// arrives in fragments, which are reassembled. Nothing on the switch changes. Although it is
// a SET on the wire, it is a read: it never passes the write gate, so it works on any firmware
// (as the provider promises for reads) even where writes are refused.
func (c *Client) ConfigBackup(ctx context.Context) (ConfigFile, error) {
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
