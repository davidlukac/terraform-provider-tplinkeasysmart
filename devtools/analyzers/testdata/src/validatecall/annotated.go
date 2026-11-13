package validatecall

import "context"

// SetPassedOn hands the value to a callee that validates it in the layer below, which the reason says.
//
// validatecall: the layer below applies the rule to the same value; checked there by its own test
func (c *Client) SetPassedOn(ctx context.Context, p Port) error {
	return c.send(ctx, p)
}

// SetOneExplained explains one parameter and not the other.
func (c *Client) SetOneExplained(
	ctx context.Context,
	// validatecall: the caller's own value, validated where it is read
	a Port,
	b Port, // want `SetOneExplained takes b \(Port has a Validate method\) and never validates it`
) error {
	return c.send(ctx, []Port{a, b})
}

// SetUnexplained has the marker and no reason: the annotation is reported, and the function is excused.
/* validatecall: */ // want `a validatecall annotation needs a reason`
func (c *Client) SetUnexplained(ctx context.Context, p Port) error {
	return c.send(ctx, p)
}
