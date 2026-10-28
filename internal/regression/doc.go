// Package regression is the scenario suite that exercises the whole stack (Terraform, the real
// provider, the escp client and the wire protocol) against one of two targets: the stateful fake
// switch of package escptest, which needs nothing and runs in CI, or switch 03, which is chosen
// by a gitignored file and is never reachable from CI. The scenarios are the same for both; a
// difference between the two is a finding in the fake or in the product.
//
// The package's own code is the harness: what a target is allowed to be, how a readout of the
// whole switch is taken and compared, and how the provider is aimed at a target. The scenarios
// live in the package's tests and run when TF_ACC is set (make regression).
//
// A safety net runs when each scenario ends (Session.GuardRestore): the VLAN mode and the port
// mirror, which Terraform cannot undo because destroying them only forgets them, are compared with
// the state recorded at the start and written back if a failed scenario left them changed. The
// scenario is failed for it; everything else is left to the readout comparison, which names it.
//
// Switch 01 is never a target: the harness refuses it by name and by address.
package regression
