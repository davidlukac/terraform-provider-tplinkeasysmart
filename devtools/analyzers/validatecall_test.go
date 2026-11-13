package main

import (
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// testPackages are the fixture packages the check is pointed at (the default is the escp-go root package).
const testPackages = `^validatecall(tests|skip|names)?$`

// validatecallFor returns the check set to look at the fixtures.
func validatecallFor(t *testing.T, flags map[string]string) *analysis.Analyzer {
	t.Helper()
	a := newValidatecall()
	if err := a.Flags.Set("packages", testPackages); err != nil {
		t.Fatal(err)
	}
	for name, value := range flags {
		if err := a.Flags.Set(name, value); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// The cases are the files of testdata/src/validatecall: the shape of escp-go#169 in historic.go (a value
// handed on with no call of its Validate, or validated after it was used) must be reported (a line marked
// `// want` is, no other line may be), and the validated shapes in fixed.go and the explained ones in
// annotated.go must stay silent.
func TestValidatecall(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), validatecallFor(t, nil), "validatecall")
}

// A package that does not match the flag is not looked at, whatever it does.
func TestValidatecallOnlyTheNamedPackages(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), newValidatecall(), "unrelated")
}

// Test files are not checked unless asked.
func TestValidatecallTestFiles(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), validatecallFor(t, nil), "validatecallskip")

	checked := validatecallFor(t, map[string]string{"tests": "true"})
	analysistest.Run(t, analysistest.TestData(), checked, "validatecalltests")
}

// The names that validate are a flag: with another name, Validate is a method like any other.
func TestValidatecallNames(t *testing.T) {
	a := validatecallFor(t, map[string]string{"names": `^Check$`})
	analysistest.Run(t, analysistest.TestData(), a, "validatecallnames")
}

// A pattern that does not compile is an error of the run, naming its flag.
func TestCompileValidatecall(t *testing.T) {
	good := validatecallConfig{packages: "a", names: "b"}
	tests := []struct {
		name string
		edit func(*validatecallConfig)
		want string
	}{
		{"packages", func(c *validatecallConfig) { c.packages = "(" }, "-validatecall.packages"},
		{"names", func(c *validatecallConfig) { c.names = "(" }, "-validatecall.names"},
		{"valid", func(c *validatecallConfig) {}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := good
			tc.edit(&cfg)
			_, err := compileValidatecall(&cfg)
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want an error naming %s, got %v", tc.want, err)
			}
		})
	}
}
