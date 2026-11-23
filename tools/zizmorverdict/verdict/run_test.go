package verdict

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// failingWriter is a writer that cannot be written to.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRun(t *testing.T) {
	good := string(list(findingJSON(allowed, "d", site, 1)))
	for _, tc := range []struct {
		name   string
		args   []string
		in     string
		status int
		out    string // what the standard output must hold (a substring), "" for nothing
		err    string // what the standard error must hold (a substring), "" for nothing
	}{
		{name: "one allowed finding, one expected", args: []string{"1"}, in: good, status: 0, out: "zizmor: no findings but the 1 allowed ones\n"},
		{name: "none, none expected", args: []string{"0"}, in: "[]", status: 0, out: "zizmor: no findings but the 0 allowed ones\n"},
		{name: "one allowed finding, none expected", args: []string{"0"}, in: good, status: 1, err: "1 of the allowed rule"},
		{name: "another rule", args: []string{"0"}, in: string(list(findingJSON(other, "d", site, 1))), status: 1, err: "template-injection"},
		{name: "an answer that is an object", args: []string{"0"}, in: "{}", status: 1, err: "not a list"},
		{name: "an empty answer", args: []string{"0"}, in: "", status: 1, err: "not a list"},
		{name: "no argument", args: nil, in: "[]", status: 2, err: "usage: zizmorverdict EXPECTED"},
		{name: "two arguments", args: []string{"0", "0"}, in: "[]", status: 2, err: "usage: zizmorverdict EXPECTED"},
		{name: "an empty argument", args: []string{""}, in: "[]", status: 1, err: "the expected count is empty"},
		{name: "a negative count", args: []string{"-1"}, in: "[]", status: 1, err: "not a plain non-negative integer"},
		{name: "a count with a space", args: []string{" 0"}, in: "[]", status: 1, err: "not a plain non-negative integer"},
		{name: "a count with a plus", args: []string{"+0"}, in: "[]", status: 1, err: "not a plain non-negative integer"},
		{name: "a leading zero", args: []string{"00"}, in: "[]", status: 1, err: "leading zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			status := Run(tc.args, strings.NewReader(tc.in), &out, &errOut)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (stdout %q, stderr %q)", status, tc.status, &out, &errOut)
			}
			if tc.out == "" && out.Len() != 0 || tc.out != "" && out.String() != tc.out {
				t.Errorf("stdout = %q, want %q", &out, tc.out)
			}
			if tc.err == "" && errOut.Len() != 0 || !strings.Contains(errOut.String(), tc.err) {
				t.Errorf("stderr = %q, want %q", &errOut, tc.err)
			}
		})
	}
}

func TestRunFailsWhenTheAnswerCannotBeRead(t *testing.T) {
	var out, errOut bytes.Buffer
	status := Run([]string{"0"}, iotest.ErrReader(errors.New("pipe closed")), &out, &errOut)
	if status != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "could not be read") || !strings.Contains(errOut.String(), "pipe closed") {
		t.Errorf("status %d, stdout %q, stderr %q; want a failure that says the answer could not be read", status, &out, &errOut)
	}
}

func TestRunFailsWhenTheAnswerStopsInTheMiddleOfAList(t *testing.T) {
	var out, errOut bytes.Buffer
	in := io.MultiReader(strings.NewReader(`[{"ident":"self-repository"},`), iotest.ErrReader(errors.New("pipe closed")))
	if status := Run([]string{"1"}, in, &out, &errOut); status != 1 || out.Len() != 0 {
		t.Errorf("status %d, stdout %q; want a failure and no output on stdout", status, &out)
	}
}

func TestRunSizeLimitOfTheAnswer(t *testing.T) {
	// An empty list padded with white space to the limit is valid; one byte more is refused unread.
	padded := func(size int) string { return "[]" + strings.Repeat(" ", size-2) }
	for _, tc := range []struct {
		name   string
		size   int
		status int
		err    string
	}{
		{name: "at the limit", size: MaxAnswer, status: 0},
		{name: "one byte over the limit", size: MaxAnswer + 1, status: 1, err: "larger than 16777216 bytes"},
		{name: "far over the limit", size: MaxAnswer + 1<<20, status: 1, err: "larger than 16777216 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			status := Run([]string{"0"}, strings.NewReader(padded(tc.size)), &out, &errOut)
			if status != tc.status {
				t.Fatalf("status = %d, want %d (stderr %.100q)", status, tc.status, &errOut)
			}
			if tc.err != "" && !strings.Contains(errOut.String(), tc.err) {
				t.Errorf("stderr = %q, want %q", &errOut, tc.err)
			}
		})
	}
}

func TestRunFailsWhenThePassCannotBeWritten(t *testing.T) {
	if status := Run([]string{"0"}, strings.NewReader("[]"), failingWriter{}, io.Discard); status != 1 {
		t.Errorf("status = %d, want 1: a verdict nobody could read is not a pass", status)
	}
}

func TestRunFailsWhenTheFailureCannotBeWritten(t *testing.T) {
	if status := Run([]string{"0"}, strings.NewReader("{}"), io.Discard, failingWriter{}); status != 1 {
		t.Errorf("status = %d, want 1", status)
	}
}

func TestRunPrintsAPassOnlyToStandardOutputAndAFailureOnlyToStandardError(t *testing.T) {
	var out, errOut bytes.Buffer
	if Run([]string{"0"}, strings.NewReader("[]"), &out, &errOut); out.Len() == 0 || errOut.Len() != 0 {
		t.Errorf("a pass: stdout %q, stderr %q", &out, &errOut)
	}
	out.Reset()
	errOut.Reset()
	if Run([]string{"1"}, strings.NewReader("[]"), &out, &errOut); out.Len() != 0 || errOut.Len() == 0 {
		t.Errorf("a failure: stdout %q, stderr %q", &out, &errOut)
	}
}
