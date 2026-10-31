//go:build tools

// Package tools pins build-time tooling (documentation generation) in go.mod. The linter and the
// vulnerability scanner are pinned in devtools/go.mod instead, so that their dependencies stay out of
// this module's go.mod (see the DEVTOOL variable in the Makefile).
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
