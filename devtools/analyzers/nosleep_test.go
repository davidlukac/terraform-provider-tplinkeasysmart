package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// The cases are the files of testdata/src/nosleep (production code): a time.Sleep is reported wherever
// it is, an annotated one and a method of another type with the name are not, and an annotation without
// a reason is reported once. time.After is not a rule of production code.
func TestNosleepInProductionCode(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newNosleep(), "nosleep")
}

// The cases are testdata/src/nosleeptests/x_test.go: a sleep is reported in a test, a time.After is
// reported unless it is the receive of a select case that fails the test, and an annotated wait is not.
func TestNosleepInTests(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newNosleep(), "nosleeptests")
}
