//go:build !windows

package probe

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stubEnvironment is an environment for the stub: its variables, and the buffers it writes to.
func stubEnvironment(vars map[string]string, environ []string) (Environment, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return Environment{
		Stdout: &out, Stderr: &errOut,
		Getenv:  func(name string) string { return vars[name] },
		Environ: func() []string { return environ },
	}, &out, &errOut
}

func TestIsStub(t *testing.T) {
	for _, tc := range []struct {
		argv0 string
		want  bool
	}{
		{"zizmor", true},
		{"/tmp/stub/zizmor", true},
		{"./zizmor", true},
		{"zizmorprobe", false},
		{"/tmp/zizmorprobe", false},
		{"zizmor.sh", false},
		{"xzizmor", false},
		{"", false},
	} {
		t.Run(tc.argv0, func(t *testing.T) {
			if got := isStub(tc.argv0); got != tc.want {
				t.Errorf("isStub(%q) = %v, want %v", tc.argv0, got, tc.want)
			}
		})
	}
}

func TestRunStubRecordsItsCallAndAnswers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		want   string
	}{
		{name: "no answer set: no findings", answer: "", want: "[]\n"},
		{name: "the answer set", answer: `[{"ident":"x"}]`, want: `[{"ident":"x"}]` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			env, out, errOut := stubEnvironment(map[string]string{stubOutVar: dir, stubJSONVar: tc.answer}, []string{"A=1", "GH_TOKEN="})
			if status := runStub([]string{"--offline", "--format", "json-v1", "."}, env); status != 0 {
				t.Fatalf("status %d: %s", status, errOut)
			}
			if out.String() != tc.want {
				t.Errorf("stdout = %q, want %q", out, tc.want)
			}
			call, err := readRecorded(dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"--offline", "--format", "json-v1", "."}; !slices.Equal(call.args, want) {
				t.Errorf("args = %q, want %q", call.args, want)
			}
			if want := []string{"A=1", "GH_TOKEN="}; !slices.Equal(call.env, want) {
				t.Errorf("env = %q, want %q", call.env, want)
			}
		})
	}
}

func TestRunStubFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, dir string)
		out   func(dir string) map[string]string
		want  string
	}{
		{name: "no directory named", out: func(string) map[string]string { return nil }, want: stubOutVar + " is not set"},
		{name: "a directory that is not there", out: func(dir string) map[string]string {
			return map[string]string{stubOutVar: filepath.Join(dir, "missing")}
		}, want: "no such file"},
		{name: "the environment file cannot be written", setup: func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Mkdir(filepath.Join(dir, recordedEnv), dirMode); err != nil {
				t.Fatal(err)
			}
		}, out: func(dir string) map[string]string { return map[string]string{stubOutVar: dir} }, want: "is a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, dir)
			}
			env, _, errOut := stubEnvironment(tc.out(dir), nil)
			if status := runStub(nil, env); status != exitError || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("status %d, stderr %q; want %d and %q", status, errOut, exitError, tc.want)
			}
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestRunStubFailsWhenItsAnswerCannotBeWritten(t *testing.T) {
	env, _, _ := stubEnvironment(map[string]string{stubOutVar: t.TempDir()}, nil)
	env.Stdout = brokenWriter{}
	if status := runStub(nil, env); status != exitError {
		t.Errorf("status %d, want %d", status, exitError)
	}
}

func TestInstallStub(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "stub")
	if err := installStub(dir, "/some/executable"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(dir, stubName)); err != nil || got != "/some/executable" {
		t.Errorf("link = %q, %v", got, err)
	}
	if err := installStub(dir, "/other"); err == nil {
		t.Error("installing over an existing stub succeeded")
	}
	file := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(file, nil, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := installStub(filepath.Join(file, "stub"), "/x"); err == nil {
		t.Error("installing under a file succeeded")
	}
}

func TestReadRecordedFailures(t *testing.T) {
	dir := t.TempDir()
	if _, err := readRecorded(dir); err == nil {
		t.Error("no arguments file, no error")
	}
	if err := os.WriteFile(filepath.Join(dir, recordedArgs), []byte("a\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := readRecorded(dir); err == nil {
		t.Error("no environment file, no error")
	}
}

func TestReadLinesDropsTheFinalNewlineOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	for _, tc := range []struct {
		name    string
		content string
		want    []string
	}{
		{name: "lines", content: "a\nb\n", want: []string{"a", "b"}},
		{name: "an empty line stays", content: "a\n\nb\n", want: []string{"a", "", "b"}},
		{name: "no final newline", content: "a\nb", want: []string{"a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.content), fileMode); err != nil {
				t.Fatal(err)
			}
			if got, err := readLines(path); err != nil || !slices.Equal(got, tc.want) {
				t.Errorf("readLines = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
