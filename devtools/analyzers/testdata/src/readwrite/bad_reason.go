package readwrite

import "context"

// SetNoReason carries the annotation without a reason: the annotation is reported, once, and the
// function stays excused (the author is told to write the reason, not to find the place again).
/* readwrite: */ // want `a readwrite annotation needs a reason`
func (c *Client) SetNoReason(ctx context.Context, v int) error {
	_, _ = c.Info(ctx)
	return c.setTLVs(ctx, v)
}
