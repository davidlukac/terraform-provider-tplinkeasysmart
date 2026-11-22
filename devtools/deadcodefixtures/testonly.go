package fixtures

// onlyATest is called by a test and by no production code: the dead-code configuration must report it
// (the tests are left out of that run), while the ordinary `unused` run of the repository, which has the
// tests on, counts it as used.
func onlyATest() {}
