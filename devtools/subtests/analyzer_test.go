package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// The cases are the functions of testdata/src/a; a line marked `// want "..."` must be reported and
// no other line may be.
func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "a")
}
