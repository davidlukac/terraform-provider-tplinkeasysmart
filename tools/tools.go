//go:build tools

// Package tools pins build-time tooling (documentation generation) in go.mod.
package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
