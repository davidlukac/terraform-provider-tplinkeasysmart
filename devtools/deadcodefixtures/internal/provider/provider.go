// Package provider stands for the provider package: the path rule of the dead-code configuration must not
// reach this directory, so what only its tests use is reported here.
package provider

// onlyAProviderTest is called by a test and by no production code, in a directory the path rule must leave
// alone: the configuration must report it. A rule that also covers this directory (a broader anchor, an
// alternation that adds it) leaves the finding missing.
func onlyAProviderTest() {}
