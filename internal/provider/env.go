package provider

import (
	"fmt"
	"os"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// envOr is the configured string, or else the value of the environment variable.
func envOr(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(env)
}

// envBoolOr is the configured bool, or else the boolean named by the environment variable, or false
// when neither is set. A variable that is set but is not a boolean is an error: a typo must not
// silently leave a safety opt-in off (or on).
func envBoolOr(v types.Bool, env string) (bool, error) {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueBool(), nil
	}
	raw := os.Getenv(env)
	if raw == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s is %q, which is not a boolean; use true or false", env, raw)
	}
	return b, nil
}
