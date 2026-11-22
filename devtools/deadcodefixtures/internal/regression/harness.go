// Package regression stands for the regression harness: the dead-code configuration leaves this directory
// out, so what only its tests use is not reported here.
package regression

// onlyAHarnessTest is called by a test and by no other code, as the seams of the harness are: it is not
// reported because the configuration excludes this directory. A path rule that stops matching reports it.
func onlyAHarnessTest() {}
