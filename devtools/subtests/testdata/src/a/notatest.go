package a

import "testing"

// A file that is not a test file is not looked at, whatever its functions are called.
func TestNotInATestFile(t *testing.T) {
	for _, in := range []string{"a"} {
		t.Log(in)
	}
}
