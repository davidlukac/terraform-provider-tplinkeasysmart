package escp

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

// randReader is the entropy source for session keys; tests replace it to
// provoke the (otherwise unreachable) read failure.
var randReader io.Reader = rand.Reader

// ensureSession discovers the switch MAC and performs the RSA session-key
// exchange once; the key is dropped on failures so the next call retries.
func (c *Client) ensureSession(ctx context.Context) error {
	if c.session != nil {
		return nil
	}
	if !c.haveSwMAC {
		if _, err := c.discover(ctx); err != nil {
			return err
		}
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	key := make([]byte, wire.SessionKeyLen)
	for i := range key {
		n, err := rand.Int(randReader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return err
		}
		key[i] = alphabet[n.Int64()]
	}
	c.session = key // the reply is already encrypted with it
	r, err := c.exchange(ctx, wire.OpSet, []wire.TLV{{Type: wire.TLVRSAKey, Value: wire.RSASessionTLV(key)}}, true, []*net.UDPAddr{c.dest})
	if err != nil {
		c.session = nil
		return fmt.Errorf("session key exchange: %w", err)
	}
	if r.err != wire.ErrCodeKeyAccepted {
		c.session = nil
		return fmt.Errorf("session key exchange: unexpected error code %d", r.err)
	}
	return nil
}

// withSession runs fn with an established session, re-establishing it once if
// the switch stops answering (session expired or switch rebooted).
func (c *Client) withSession(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		if err := c.ensureSession(ctx); err != nil {
			return err
		}
		err := fn()
		if errors.Is(err, ErrNoReply) && attempt == 0 {
			c.session = nil
			continue
		}
		return err
	}
}
