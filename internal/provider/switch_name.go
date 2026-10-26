package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	// idSeparator joins a switch name and an id, in `id` attributes and import ids.
	idSeparator = ":"

	maxSwitchNameLen = 63

	// switchNamePattern limits switch names to what is safe in addresses and ids: no separator,
	// no spaces.
	switchNamePattern = `^[A-Za-z0-9][A-Za-z0-9_-]*$`
)

var switchNameRegexp = regexp.MustCompile(switchNamePattern)

// switchName is the name of a switch in the provider's `switches` map, as a resource's `switch`
// argument or the prefix of an id selects it. The empty name is the provider's own switch (the
// default), the same as no name. Everything about what a name may be, and how it joins an id and
// comes off one again, is here.
type switchName string

// switchNameOf is the name in a `switch` argument; null, unknown and empty are the default switch.
func switchNameOf(v types.String) switchName {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return switchName(v.ValueString())
}

// validate reports whether n can be the key of the `switches` map: 1 to maxSwitchNameLen letters,
// digits, hyphens and underscores, starting with a letter or a digit.
func (n switchName) validate() error {
	if len(n) < 1 || len(n) > maxSwitchNameLen || !switchNameRegexp.MatchString(string(n)) {
		return fmt.Errorf("a switch name must start with a letter or digit and contain only letters, digits, hyphens and underscores, and be 1 to %d characters long", maxSwitchNameLen)
	}
	return nil
}

// qualify prefixes id with the name, so ids stay unique across switches and equal the import id.
// The default switch's ids are unchanged.
func (n switchName) qualify(id string) string {
	if n == "" {
		return id
	}
	return string(n) + idSeparator + id
}

// splitQualified separates an optional `switch:` prefix from an id (an import id, or what qualify made).
func splitQualified(id string) (n switchName, rest string) {
	if i := strings.Index(id, idSeparator); i >= 0 {
		return switchName(id[:i]), id[i+len(idSeparator):]
	}
	return "", id
}

// validSwitchName is the validator of every place a switch name is written: the `switch` argument
// and the keys of the `switches` map.
func validSwitchName() []validator.String { return []validator.String{switchNameValidator{}} }

type switchNameValidator struct{}

func (switchNameValidator) Description(context.Context) string {
	return "must be a switch name: letters, digits, hyphens and underscores, starting with a letter or digit"
}

func (v switchNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (switchNameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if err := switchName(req.ConfigValue.ValueString()).validate(); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid switch name", err.Error())
	}
}
