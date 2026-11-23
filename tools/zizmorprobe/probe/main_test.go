package probe

import (
	"context"
	"os"
	"testing"
)

// TestMain makes the test binary the stub zizmor when it is run under that name: the probes put a link named
// zizmor to the running program first on the PATH.
func TestMain(m *testing.M) {
	if len(os.Args) > 0 && isStub(os.Args[0]) {
		os.Exit(Run(context.Background(), os.Args, ProcessEnvironment()))
	}
	os.Exit(m.Run())
}
