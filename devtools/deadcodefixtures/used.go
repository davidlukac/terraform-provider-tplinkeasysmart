// Package fixtures holds what the dead-code configuration (.golangci-deadcode.yml) must and must not
// report: see README.md.
package fixtures

// Exported is part of the API of its package: an exported identifier that nothing calls is not reported.
func Exported() { helper() }

// helper is called by production code: clean.
func helper() {}
