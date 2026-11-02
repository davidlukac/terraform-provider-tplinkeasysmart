package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{"go_module":"m","files":[
 {"file_name":"a.go","mutations":[{"type":"CONDITIONALS_NEGATION","status":"KILLED","line":3,"column":4},{"type":"ARITHMETIC_BASE","status":"LIVED","line":9,"column":2}]},
 {"file_name":"b.go","mutations":[{"type":"CONDITIONALS_BOUNDARY","status":"LIVED","line":1,"column":1},{"type":"CONDITIONALS_BOUNDARY","status":"KILLED","line":2,"column":1},{"type":"CONDITIONALS_BOUNDARY","status":"NOT COVERED","line":5,"column":1}]}
]}`

func runWith(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

func writeReport(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mutation.json")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTheSummaryCountsEachOutcomeAndListsTheSurvivors(t *testing.T) {
	code, out, errOut := runWith(t, writeReport(t, sample))
	want := "### Mutation testing\n\nKILLED: 2, LIVED: 2, NOT COVERED: 1\n\n" +
		"Surviving mutants (a behavior no test pins; an equivalent mutant is not a reason to bend a test):\n\n" +
		"- `a.go:9:2` ARITHMETIC_BASE\n- `b.go:1:1` CONDITIONALS_BOUNDARY\n"
	if code != exitOK || errOut != "" || out != want {
		t.Fatalf("code %d, stderr %q, stdout:\n%s\nwant:\n%s", code, errOut, out, want)
	}
}

func TestARunWithoutSurvivorsListsNone(t *testing.T) {
	_, out, _ := runWith(t, writeReport(t, `{"files":[{"file_name":"a.go","mutations":[{"type":"X","status":"KILLED","line":1,"column":1}]}]}`))
	if !strings.Contains(out, "KILLED: 1") || strings.Contains(out, "Surviving") {
		t.Fatalf("got %q", out)
	}
}

func TestAnEmptyReportSaysThereWasNothingToMutate(t *testing.T) {
	_, out, _ := runWith(t, writeReport(t, `{"files":[]}`))
	if !strings.Contains(out, "No mutants in scope.") {
		t.Fatalf("got %q", out)
	}
}

// A job summary must never fail the job: a missing or unreadable report is said, not an error.
func TestAMissingOrBrokenReportIsSaidAndNotAnError(t *testing.T) {
	code, out, _ := runWith(t, filepath.Join(t.TempDir(), "absent.json"))
	if code != exitOK || !strings.Contains(out, "No report") {
		t.Fatalf("missing: code %d, %q", code, out)
	}
	code, out, _ = runWith(t, writeReport(t, `{`))
	if code != exitOK || !strings.Contains(out, "cannot be read") {
		t.Fatalf("broken: code %d, %q", code, out)
	}
}

func TestWrongArgumentsAreAUsageError(t *testing.T) {
	for _, args := range [][]string{nil, {"a", "b"}} {
		if code, _, errOut := runWith(t, args...); code != exitUsage || !strings.Contains(errOut, "usage") {
			t.Errorf("%v: code %d, %q", args, code, errOut)
		}
	}
}
