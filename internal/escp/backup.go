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

// ErrInvalidConfig means the reply was not a complete switch configuration file: it did not
// start with the configuration magic, a packet was missing, or two downloads of it differed. The
// message says which. ConfigBackup repeats the download before it returns this.
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
//
// The result is complete or there is none. The replies are UDP: one can be lost, and the
// fragments carry no index, total or checksum (the web UI is different: TCP, with a declared
// Content-Length). A download is therefore accepted only when it is structurally complete (it
// starts with the magic, the last fragment arrived, the packets before it are equally long, see
// assembleConfig) and a second download, requested separately, is byte for byte the same. Anything
// else is repeated, up to Config.Retries+1 rounds, and then reported as ErrInvalidConfig. A
// configuration that changes while it is read fails the same way. A rejection, a missing reply
// and a cancelled context end the call at once.
func (c *Client) ConfigBackup(ctx context.Context) (ConfigFile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []byte
	err := c.withSession(ctx, func() error {
		var err error
		out, err = c.verifiedConfig(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// verifiedConfig downloads the file twice per round until both downloads are complete and equal.
func (c *Client) verifiedConfig(ctx context.Context) ([]byte, error) {
	var last error
	for round := 0; round <= c.cfg.Retries; round++ {
		first, err := c.downloadConfig(ctx)
		if err == nil {
			var second []byte
			if second, err = c.downloadConfig(ctx); err == nil {
				if bytes.Equal(first, second) {
					return first, nil
				}
				err = fmt.Errorf("%w: two downloads differ (%d and %d bytes)", ErrInvalidConfig, len(first), len(second))
			}
		}
		if !errors.Is(err, ErrInvalidConfig) {
			return nil, err
		}
		c.log.WarnContext(ctx, "configuration download not usable; repeating", "round", round+1, "rounds", c.cfg.Retries+1, "reason", err.Error())
		last = err // a cancelled context is reported by the next download, which checks it before it sends
	}
	return nil, last
}

// downloadConfig is one request for the file, checked by assembleConfig.
func (c *Client) downloadConfig(ctx context.Context) ([]byte, error) {
	req := []wire.TLV{
		{Type: wire.TLVUsername, Value: append([]byte(c.cfg.Username), 0)},
		{Type: wire.TLVPassword, Value: append([]byte(c.cfg.Password.Reveal()), 0)},
		{Type: wire.TLVConfigFile, Value: nil},
	}
	parts, err := c.exchangeFragments(ctx, wire.OpSet, req, wire.TLVConfigFile)
	if err != nil {
		return nil, err
	}
	return assembleConfig(parts)
}

// assembleConfig joins the packets of one download, in order, and refuses what cannot be the whole
// file: no magic (the first packet is missing, or this is not a configuration), or packets of
// unequal length before the last (the switch fills every packet but the last, 1400 bytes in a
// 2258-byte file; a shorter one in front means a neighbour was lost), or a last packet longer than
// the others. A lost middle packet of equal length cannot be seen here; the second download of
// ConfigBackup catches that.
func assembleConfig(parts [][]byte) ([]byte, error) {
	var file []byte
	for _, p := range parts {
		file = append(file, p...)
	}
	if len(file) <= len(wire.ConfigMagic) || !bytes.HasPrefix(file, wire.ConfigMagic) {
		return nil, fmt.Errorf("%w (%d bytes in %d packets, no configuration magic at the start)", ErrInvalidConfig, len(file), len(parts))
	}
	for i, p := range parts[:len(parts)-1] {
		if len(p) != len(parts[0]) {
			return nil, fmt.Errorf("%w (packet %d has %d bytes, packet 1 has %d)", ErrInvalidConfig, i+1, len(p), len(parts[0]))
		}
	}
	if last := parts[len(parts)-1]; len(last) > len(parts[0]) {
		return nil, fmt.Errorf("%w (the last packet has %d bytes, more than the first, %d)", ErrInvalidConfig, len(last), len(parts[0]))
	}
	return file, nil
}
