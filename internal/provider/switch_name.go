package provider

import (
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

	// defaultSwitchName selects the provider's own switch (`host`) by name. It is the only way to
	// do so in a configuration that has a `switches` map, where an object without `switch` is an
	// error, and it cannot be the key of that map.
	defaultSwitchName = "default"

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

// switchNameOf is the name in a `switch` argument; null, unknown and empty are no name at all
// (the provider's own switch when it is the only one), `default` names the provider's own switch.
func switchNameOf(v types.String) switchName {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return switchName(v.ValueString())
}

// validateKey is validate for the key of the `switches` map, which may not be the reserved name
// of the provider's own switch.
func (n switchName) validateKey() error {
	if n == defaultSwitchName {
		return fmt.Errorf("%q is reserved for the provider's own switch (`host`); choose another name", defaultSwitchName)
	}
	return n.validate()
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
// The ids of the provider's own switch (no name, or `default`) are unchanged, whichever way it is selected.
func (n switchName) qualify(id string) string {
	if n == "" || n == defaultSwitchName {
		return id
	}
	return string(n) + idSeparator + id
}

// splitQualified separates an optional `switch:` prefix from an id (an import id, or what qualify made).
func splitQualified(id string) (n switchName, rest string) {
	if before, after, ok := strings.Cut(id, idSeparator); ok {
		return switchName(before), after
	}
	return "", id
}

// validSwitchName is the validator of every place a switch name is written: the `switch` argument
// and the keys of the `switches` map.
func validSwitchName() []validator.String {
	return []validator.String{stringRule{
		description: "must be a switch name: letters, digits, hyphens and underscores, starting with a letter or digit",
		summary:     "Invalid switch name",
		check:       func(s string) error { return switchName(s).validate() },
	}}
}

// validSwitchKey is the validator of the keys of the `switches` map.
func validSwitchKey() []validator.String {
	return []validator.String{stringRule{
		description: "must be a switch name: letters, digits, hyphens and underscores, starting with a letter or digit, and not `default`",
		summary:     "Invalid switch name",
		check:       func(s string) error { return switchName(s).validateKey() },
	}}
}
