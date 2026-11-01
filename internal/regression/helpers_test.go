package regression

import (
	"fmt"
	"testing"

	"github.com/davidlukac/escp-go"
)

func igmpConfig(enabled, suppression bool) escp.IGMPConfig {
	return escp.IGMPConfig{Enabled: enabled, ReportSuppression: suppression}
}

// fatalRecorder records Fatalf instead of ending the test, then stops the calling goroutine's
// function the way testing.T does (runtime.Goexit would end the whole test, so it panics and
// the caller recovers).
type fatalRecorder struct {
	testing.TB
	failed bool
	msg    string
}

func (f *fatalRecorder) Helper() {}
func (f *fatalRecorder) Fatalf(format string, args ...any) {
	f.failed, f.msg = true, fmt.Sprintf(format, args...)
	panic(f.msg)
}

// errorRecorder collects the Errorf calls of a test double, so a test can assert that the code
// under test failed the test it was given without failing its own.
type errorRecorder struct {
	testing.TB
	errors []string
}

func (e *errorRecorder) Helper() {}
func (e *errorRecorder) Errorf(format string, args ...any) {
	e.errors = append(e.errors, fmt.Sprintf(format, args...))
}
